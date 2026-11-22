package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeManualCertbot is a certbot that answers a manual DNS order the way the
// real one does: it runs the auth hook once per name with CERTBOT_DOMAIN and
// CERTBOT_VALIDATION set, writes the lineage under --cert-name, then runs the
// cleanup hook. It returns its argv log.
func fakeManualCertbot(t *testing.T, letsencrypt string, names []string) string {
	t.Helper()
	bin, stage := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "argv.log")
	writeExecutable(t, filepath.Join(bin, "certbot"), fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1" in
--version) echo "certbot 9.9.9"; exit 0 ;;
plugins) echo "* webroot"; echo "Interfaces: Authenticator, Plugin"; exit 0 ;;
certonly)
  name=""; auth=""; cleanup=""; dry=""
  while [ $# -gt 0 ]; do
    case "$1" in
    --cert-name) name="$2"; shift ;;
    --manual-auth-hook) auth="$2"; shift ;;
    --manual-cleanup-hook) cleanup="$2"; shift ;;
    --dry-run) dry=1 ;;
    esac
    shift
  done
  CERTBOT_DOMAIN=acme.just-dashboard.com CERTBOT_VALIDATION=value-one sh -c "$auth" || exit 1
  CERTBOT_DOMAIN=acme.just-dashboard.com CERTBOT_VALIDATION=value-two sh -c "$auth" || exit 1
  if [ -z "$dry" ]; then mkdir -p %q/live/"$name"; cp %q/*.pem %q/live/"$name"/; fi
  CERTBOT_DOMAIN=acme.just-dashboard.com CERTBOT_VALIDATION=value-one sh -c "$cleanup"
  CERTBOT_DOMAIN=acme.just-dashboard.com CERTBOT_VALIDATION=value-two sh -c "$cleanup"
  echo "Successfully received certificate."
  exit 0 ;;
esac
exit 1
`, log, letsencrypt, stage, letsencrypt))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	forgetCertbotRuntime()
	t.Cleanup(forgetCertbotRuntime)
	writeDeploymentCertificate(t, stage, names)
	return log
}

// fakeJD is the hook binary: it records what it was run with.
func fakeJD(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "jd.log")
	path := filepath.Join(dir, "jd")
	writeExecutable(t, path, fmt.Sprintf("#!/bin/sh\necho \"$* $CERTBOT_DOMAIN $CERTBOT_VALIDATION\" >> %q\n", log))
	previous := AppDomainHookBinary
	AppDomainHookBinary = path
	t.Cleanup(func() { AppDomainHookBinary = previous })
	return log
}

func TestTheAppDomainWildcardIsOrderedThroughTheJDHooksAndReadBack(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	t.Setenv("JD_ACME_DIRECTORY", "")
	names := []string{"*.acme.just-dashboard.com", "acme.just-dashboard.com"}
	argv := fakeManualCertbot(t, letsencrypt, names)
	hooks := fakeJD(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	order := AppDomainOrder{Handle: "acme", Zone: "just-dashboard.com", Credentials: "/var/lib/just-dashboard/app-domain-hook.json"}

	if state := service.AppDomainCertificate(context.Background(), "acme", "just-dashboard.com"); state.State != "missing" {
		t.Fatalf("before = %+v", state)
	}
	result, err := service.IssueAppDomainCertificate(context.Background(), order, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != CertificateIssued || result.Name != "jd-app-domain-acme" || result.Method != "app-domain" {
		t.Fatalf("result = %+v", result)
	}
	line := certonlyLine(t, argv)
	for _, want := range []string{
		"certonly --manual --preferred-challenges dns",
		"--manual-auth-hook " + AppDomainHookBinary + " app-domain challenge add --credentials /var/lib/just-dashboard/app-domain-hook.json",
		"--manual-cleanup-hook " + AppDomainHookBinary + " app-domain challenge remove --credentials /var/lib/just-dashboard/app-domain-hook.json",
		"--cert-name jd-app-domain-acme", "--keep-until-expiring", "-d *.acme.just-dashboard.com -d acme.just-dashboard.com",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("certbot ran %q, missing %q", line, want)
		}
	}
	if strings.Contains(line, "--webroot") || strings.Contains(line, "--standalone") || strings.Contains(line, "--nginx") {
		t.Fatalf("an HTTP challenge was asked for: %q", line)
	}
	ran, _ := os.ReadFile(hooks)
	for _, want := range []string{
		"app-domain challenge add --credentials /var/lib/just-dashboard/app-domain-hook.json acme.just-dashboard.com value-one",
		"app-domain challenge add --credentials /var/lib/just-dashboard/app-domain-hook.json acme.just-dashboard.com value-two",
		"app-domain challenge remove --credentials /var/lib/just-dashboard/app-domain-hook.json acme.just-dashboard.com value-one",
	} {
		if !strings.Contains(string(ran), want) {
			t.Fatalf("hooks ran:\n%s\nmissing %q", ran, want)
		}
	}
	state := service.AppDomainCertificate(context.Background(), "acme", "just-dashboard.com")
	if state.State != "valid" || state.ExpiresAt == 0 || !strings.HasSuffix(state.CertPath, "live/jd-app-domain-acme/fullchain.pem") {
		t.Fatalf("after = %+v", state)
	}
	// The wildcard is what every name under the handle resolves to.
	cert, _, err := service.ResolveDeploymentCertificate(context.Background(), []string{"shop.acme.just-dashboard.com"})
	if err != nil || cert != state.CertPath {
		t.Fatalf("resolve = %q, %v", cert, err)
	}
}

func TestAppDomainDryRunAndForceUseTheirOwnFlags(t *testing.T) {
	t.Parallel()
	order := AppDomainOrder{Handle: "acme", Zone: "just-dashboard.com", Credentials: "/c.json", DryRun: true}
	dry := strings.Join(appDomainIssueArgs(order, true, ACMEDirectory{}), " ")
	if !strings.Contains(dry, "--dry-run") || strings.Contains(dry, "--keep-until-expiring") || strings.Contains(dry, "--register-unsafely-without-email") {
		t.Fatalf("dry run = %q", dry)
	}
	order.DryRun, order.Force = false, true
	if force := strings.Join(appDomainIssueArgs(order, false, ACMEDirectory{}), " "); !strings.Contains(force, "--force-renewal") || !strings.Contains(force, "--register-unsafely-without-email") {
		t.Fatalf("force = %q", force)
	}
}

func TestAppDomainOrderRefusesWithoutTheHookBinary(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	argv := fakeManualCertbot(t, letsencrypt, []string{"*.acme.just-dashboard.com", "acme.just-dashboard.com"})
	previous := AppDomainHookBinary
	AppDomainHookBinary = filepath.Join(root, "no-such-jd")
	t.Cleanup(func() { AppDomainHookBinary = previous })
	service := New(root, filepath.Join(root, "Caddyfile"))
	_, err := service.IssueAppDomainCertificate(context.Background(), AppDomainOrder{Handle: "acme", Zone: "just-dashboard.com", Credentials: "/c.json"}, nil)
	if !errors.Is(err, ErrAppDomainHookMissing) || !strings.Contains(err.Error(), "install-cli") {
		t.Fatalf("err = %v", err)
	}
	if raw, _ := os.ReadFile(argv); strings.Contains(string(raw), "certonly") {
		t.Fatal("certbot was asked to order without a hook to answer it")
	}
}

func TestTheTXTWaitAsksEveryAuthoritativeNameserver(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]int{}
	published := map[string]bool{"192.0.2.1": true}
	restore := UseAppDomainDNSForTest(
		func(_ context.Context, name string) ([]*net.NS, error) {
			if name == "just-dashboard.com" {
				return []*net.NS{{Host: "ada.ns.example.net."}, {Host: "bob.ns.example.net."}}, nil
			}
			return nil, errors.New("no such zone")
		},
		func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host == "ada.ns.example.net" {
				return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}, nil
			}
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.2")}}, nil
		},
		func(_ context.Context, server, name string) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			asked[server]++
			if name != "_acme-challenge.acme.just-dashboard.com" {
				return nil, errors.New("wrong name")
			}
			// The second server catches up on its third question.
			if asked[server] >= 3 {
				published[server] = true
			}
			if published[server] {
				return []string{"other", "the-value"}, nil
			}
			return []string{}, nil
		},
		time.Millisecond,
	)
	defer restore()
	if err := WaitForAuthoritativeTXT(t.Context(), "_acme-challenge.acme.just-dashboard.com", "the-value"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if asked["192.0.2.2"] < 3 || asked["192.0.2.1"] != 1 {
		t.Fatalf("asked = %v", asked)
	}
	mu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	err := WaitForAuthoritativeTXT(ctx, "_acme-challenge.acme.just-dashboard.com", "never-published")
	if err == nil || !strings.Contains(err.Error(), "ada.ns.example.net") || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnAppDomainLineageRenewsOnlyThroughJD(t *testing.T) {
	t.Parallel()
	good := renewalConf{Name: "jd-app-domain-acme", Authenticator: "manual",
		ManualAuthHook:    "/usr/local/bin/jd app-domain challenge add --credentials /x",
		ManualCleanupHook: "/usr/local/bin/jd app-domain challenge remove --credentials /x"}
	previous := AppDomainHookBinary
	if previous != "/usr/local/bin/jd" {
		t.Skip("another test has the hook binary")
	}
	if p := appDomainHookProblem(good, map[string]bool{}); p != "" {
		t.Fatalf("good lineage: %s", p)
	}
	if p := appDomainHookProblem(good, map[string]bool{"/usr/local/bin/jd": true}); !strings.Contains(p, "install-cli") {
		t.Fatalf("missing jd: %q", p)
	}
	other := good
	other.ManualAuthHook = "/root/hook.sh"
	if p := appDomainHookProblem(other, map[string]bool{}); !strings.Contains(p, "not jd's") {
		t.Fatalf("foreign hook: %q", p)
	}
	other = good
	other.Authenticator = "dns-cloudflare"
	if p := appDomainHookProblem(other, map[string]bool{}); !strings.Contains(p, "manual plugin") {
		t.Fatalf("other plugin: %q", p)
	}
}

func appDomainRoute(t *testing.T) DeploymentRoute {
	t.Helper()
	root := t.TempDir()
	own, ownKey := writeDeploymentCertificate(t, filepath.Join(mkdir(t, root, "own")), []string{"shop.example.test"})
	wild, wildKey := writeDeploymentCertificate(t, filepath.Join(mkdir(t, root, "jd-app-domain-acme")), []string{"*.acme.just-dashboard.com", "acme.just-dashboard.com"})
	return DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"shop.example.test", "shop.acme.just-dashboard.com"},
		Upstream: "http://127.0.0.1:32123", TLS: true, ForceHTTPS: true, CertPath: own, KeyPath: ownKey,
		Certificates: []RouteCertificate{{Domains: []string{"shop.acme.just-dashboard.com"}, CertPath: wild, KeyPath: wildKey}},
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAnNginxRouteServesItsAppNameWithTheWildcardInAServerOfItsOwn(t *testing.T) {
	t.Parallel()
	route := appDomainRoute(t)
	if err := validateRouteCertificates(route, false); err != nil {
		t.Fatal(err)
	}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	if !strings.Contains(content, "server_name shop.example.test shop.acme.just-dashboard.com;\n") {
		t.Fatalf("the redirect to HTTPS does not cover both names:\n%s", content)
	}
	own := strings.Index(content, "    server_name shop.example.test;\n")
	extra := strings.Index(content, "    server_name shop.acme.just-dashboard.com;\n")
	if own < 0 || extra < own {
		t.Fatalf("the names are not in servers of their own:\n%s", content)
	}
	if !strings.Contains(content[own:extra], route.CertPath) || !strings.Contains(content[extra:], route.Certificates[0].CertPath) || strings.Contains(content[extra:], route.CertPath+";") {
		t.Fatalf("each server does not serve its own certificate:\n%s", content)
	}
	if strings.Count(content, "proxy_pass http://127.0.0.1:32123;") != 2 {
		t.Fatalf("the app name's server does not carry the application:\n%s", content)
	}
}

func TestACaddyRouteServesItsAppNameWithACopyOfTheWildcard(t *testing.T) {
	t.Parallel()
	route := appDomainRoute(t)
	// Caddy's own certificate for the operator's name; the app name's copy
	// is the route's one copy.
	route.CertPath = filepath.Join(dockerCaddyRoot, "caddy-0123456789abcdef01234567", "fullchain.pem")
	content, err := renderDockerCaddyRoute(route, "http://172.17.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := dockerCaddyCertificateCopy(route.Certificates[0].CertPath)
	block := strings.Index(content, "shop.acme.just-dashboard.com {\n")
	if block < 0 || !strings.Contains(content[block:], tlsCopyLine(hash)) {
		t.Fatalf("the app name is not served from the wildcard's copy:\n%s", content)
	}
	if own := strings.Index(content, "shop.example.test {\n"); own < 0 || strings.Contains(content[own:block], "tls \"") {
		t.Fatalf("the operator's name lost Caddy's own certificate:\n%s", content)
	}
	if source, ok := certificateSourceFor(route); !ok || source.CertPath != route.Certificates[0].CertPath {
		t.Fatalf("the copy's source = %+v, %v", source, ok)
	}
	// Two copies in one route are more than the refresh keeps current.
	two := appDomainRoute(t)
	if _, err := renderDockerCaddyRoute(two, "http://172.17.0.2:3000"); err == nil || !strings.Contains(err.Error(), "at most one certificate copy") {
		t.Fatalf("two copies = %v", err)
	}
}

func TestNoDeploymentOrderIsPlacedForANameUnderTheAppDomainZone(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	argv := fakeIssuingCertbot(t, letsencrypt, []string{"shop.acme.just-dashboard.com"}, "webroot")
	service := New(root, filepath.Join(root, "Caddyfile"))
	service.SetAppDomainZone(func() string { return "just-dashboard.com" })
	for _, names := range [][]string{{"shop.acme.just-dashboard.com"}, {"shop.example.test", "x.other.just-dashboard.com"}} {
		if _, err := service.EnsureDeploymentCertificate(context.Background(), names, nil); !errors.Is(err, ErrAppDomainName) {
			t.Fatalf("%v: err = %v", names, err)
		}
	}
	if raw, _ := os.ReadFile(argv); strings.Contains(string(raw), "certonly") {
		t.Fatalf("certbot ordered a name under the zone:\n%s", raw)
	}
}
