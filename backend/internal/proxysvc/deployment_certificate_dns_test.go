package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nameservers answers NS lookups from a table: a zone's servers by its name,
// and nothing — as a resolver says for a name that is not a zone apex — for
// everything else. asked records every name it was given.
func nameservers(table map[string][]string, asked *[]string) nsLookup {
	return func(_ context.Context, name string) ([]*net.NS, error) {
		if asked != nil {
			*asked = append(*asked, name)
		}
		hosts, ok := table[name]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
		}
		records := make([]*net.NS, 0, len(hosts))
		for _, host := range hosts {
			records = append(records, &net.NS{Host: host + "."})
		}
		return records, nil
	}
}

func providers(keys ...string) []DNSProvider {
	out := []DNSProvider{}
	for _, key := range keys {
		provider, _ := DNSProviderFor(key)
		out = append(out, provider)
	}
	return out
}

// Credentials are kept per provider, not per zone, so the zone's own
// nameservers decide whose are used; the one usable provider decides when
// they cannot; and anything else is a refusal that says what to save and
// where, never a guess the authority would count as a failed validation.
func TestChooseDeploymentDNSProviderFromTheZonesNameservers(t *testing.T) {
	cloudflareZone := map[string][]string{"example.com": {"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}}
	digitalOceanZone := map[string][]string{"example.com": {"ns1.digitalocean.com", "ns2.digitalocean.com"}}
	ownZone := map[string][]string{"example.com": {"ns1.example.net"}}
	all := map[string]bool{"dns-cloudflare": true, "dns-digitalocean": true, "dns-rfc2136": true, "dns-gandi": true}
	for _, tc := range []struct {
		name    string
		zone    map[string][]string
		saved   []DNSProvider
		plugins map[string]bool
		want    string
		refusal []string
	}{
		{name: "the zone's provider among several", zone: cloudflareZone,
			saved: providers("cloudflare", "gandi"), plugins: all, want: "cloudflare"},
		{name: "the only one, nameservers of the operator's own", zone: ownZone,
			saved: providers("rfc2136"), plugins: all, want: "rfc2136"},
		{name: "the only one, nameservers unreadable", zone: nil,
			saved: providers("cloudflare"), plugins: all, want: "cloudflare"},
		{name: "the only usable one", zone: ownZone,
			saved: providers("gandi", "rfc2136"), plugins: map[string]bool{"dns-rfc2136": true}, want: "rfc2136"},
		{name: "nothing saved", zone: cloudflareZone, saved: nil, plugins: all,
			refusal: []string{"*.preview.example.com is a wildcard", "no DNS provider has credentials saved", "(/proxy/certificates)"}},
		{name: "another provider hosts the zone", zone: digitalOceanZone,
			saved: providers("cloudflare"), plugins: all,
			refusal: []string{"example.com is hosted by DigitalOcean", "ns1.digitalocean.com", "Save DigitalOcean's API token", "(/proxy/certificates)"}},
		{name: "the zone's provider has no plugin", zone: cloudflareZone,
			saved: providers("cloudflare"), plugins: map[string]bool{"dns-rfc2136": true},
			refusal: []string{"Cloudflare has credentials saved", "has no dns-cloudflare plugin", "Install it under"}},
		{name: "two candidates and nothing to tell them apart", zone: ownZone,
			saved: providers("cloudflare", "rfc2136"), plugins: all,
			refusal: []string{"Cloudflare and RFC 2136 (BIND, Knot, PowerDNS) all have credentials saved",
				"example.com's nameservers (ns1.example.net) do not say which", "Issue *.preview.example.com once"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := chooseDeploymentDNSProvider(context.Background(), "*.preview.example.com",
				tc.saved, tc.plugins, "the host's, certbot 2.11.0", nameservers(tc.zone, nil))
			if tc.refusal == nil {
				if err != nil || got.Key != tc.want {
					t.Fatalf("chose %q, %v; want %q", got.Key, err, tc.want)
				}
				return
			}
			if err == nil || !errors.Is(err, ErrCertificateWildcard) {
				t.Fatalf("chose %q, %v; want a wildcard refusal", got.Key, err)
			}
			for _, want := range tc.refusal {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not say %q", err, want)
				}
			}
		})
	}
}

// The zone is found by walking up from the wildcard's base, and the walk
// stops short of the top-level domain: "com" is nobody's zone to write to.
func TestZoneNameserversWalksUpToTheZoneAndNoFurther(t *testing.T) {
	var asked []string
	zone, hosts := zoneNameservers(context.Background(), "pr.preview.example.com",
		nameservers(map[string][]string{"example.com": {"ada.ns.cloudflare.com"}}, &asked))
	if zone != "example.com" || len(hosts) != 1 || hosts[0] != "ada.ns.cloudflare.com" {
		t.Fatalf("zone = %q %v", zone, hosts)
	}
	if strings.Join(asked, " ") != "pr.preview.example.com preview.example.com example.com" {
		t.Fatalf("asked %v", asked)
	}
	asked = nil
	if zone, _ := zoneNameservers(context.Background(), "nowhere.test", nameservers(nil, &asked)); zone != "" {
		t.Fatalf("found %q for a name with no zone", zone)
	}
	if strings.Join(asked, " ") != "nowhere.test" {
		t.Fatalf("asked %v, which reaches the top-level domain", asked)
	}
}

// fakeDNSCertbot is a certbot with the named plugins whose certonly
// "issues" by putting a certificate for *.preview.example.test where certbot
// would, under the first name's lineage, and records its argv.
func fakeDNSCertbot(t *testing.T, letsencrypt string, plugins ...string) string {
	t.Helper()
	bin, stage := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "argv.log")
	var listing strings.Builder
	for _, p := range plugins {
		fmt.Fprintf(&listing, "* %s\nDescription: the %s plugin\nInterfaces: Authenticator, Plugin\n\n", p, p)
	}
	writeExecutable(t, filepath.Join(bin, "certbot"), fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1" in
--version) echo "certbot 9.9.9"; exit 0 ;;
plugins) cat <<'LIST'
%sLIST
exit 0 ;;
certonly)
  name=""
  while [ $# -gt 0 ]; do
    if [ "$1" = "-d" ] && [ -z "$name" ]; then name="${2#\*.}"; fi
    shift
  done
  mkdir -p %q/live/"$name"
  cp %q/*.pem %q/live/"$name"/
  echo "Successfully received certificate."
  exit 0 ;;
esac
exit 1
`, log, listing.String(), letsencrypt, stage, letsencrypt))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	forgetCertbotRuntime()
	t.Cleanup(forgetCertbotRuntime)
	writeDeploymentCertificate(t, stage, []string{"*.preview.example.test", "preview.example.test"})
	return log
}

// A run that serves a wildcard orders it over DNS-01 through the credentials
// the Certificates page saved, with the argv that page builds — the file's
// path, never its contents — and reads the certificate back before it says
// so. A second deployment onto the wildcard, and a preview under it, ask the
// authority for nothing.
func TestDeploymentWildcardIsIssuedOverDNSThenReused(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	restore := UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported"))
	t.Cleanup(restore)
	t.Setenv("JD_ACME_DIRECTORY", "")
	previous := deploymentNSLookup
	deploymentNSLookup = nameservers(map[string][]string{"example.test": {"ada.ns.cloudflare.com"}}, nil)
	t.Cleanup(func() { deploymentNSLookup = previous })
	argv := fakeDNSCertbot(t, letsencrypt, "webroot", "dns-cloudflare")
	const token = "dns_cloudflare_api_token = secret-zone-token"
	if _, err := WriteDNSCredentials("cloudflare", token); err != nil {
		t.Fatal(err)
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	var logged []string
	result, err := service.EnsureDeploymentCertificate(context.Background(),
		[]string{"*.preview.example.test"}, func(_, line string) { logged = append(logged, line) })
	if err != nil {
		t.Fatalf("issue: %v\n%s", err, strings.Join(logged, "\n"))
	}
	if result.Outcome != CertificateIssued || result.Method != "dns-cloudflare" || result.DNSProvider != "Cloudflare" ||
		result.Name != "preview.example.test" || result.Registered {
		t.Fatalf("result = %+v", result)
	}
	raw, _ := os.ReadFile(argv)
	var order string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "certonly ") {
			order = line
		}
	}
	for _, want := range []string{
		"--dns-cloudflare --dns-cloudflare-credentials " + filepath.Join(letsencrypt, "jd-dns", "cloudflare.ini") +
			" --dns-cloudflare-propagation-seconds 30",
		"--non-interactive --agree-tos --keep-until-expiring --no-eff-email --register-unsafely-without-email",
		"-d *.preview.example.test",
	} {
		if !strings.Contains(order, want) {
			t.Fatalf("certbot ran %q, missing %q", order, want)
		}
	}
	if strings.Contains(string(raw)+strings.Join(logged, "\n"), "secret-zone-token") {
		t.Fatal("the saved token reached certbot's argv or the run's transcript")
	}

	for _, name := range []string{"*.preview.example.test", "pr-7.preview.example.test"} {
		again, err := service.EnsureDeploymentCertificate(context.Background(), []string{name}, nil)
		if err != nil || again.Outcome != CertificateReused || again.Name != "preview.example.test" {
			t.Fatalf("%s = %+v, %v; want the wildcard reused", name, again, err)
		}
	}
	raw, _ = os.ReadFile(argv)
	if strings.Count(string(raw), "certonly ") != 1 {
		t.Fatalf("certbot ordered again:\n%s", raw)
	}
}

// With no credentials saved the run refuses before certbot is asked, says
// where they are saved, and is told apart from an order the authority turned
// down; the preflight read gives the same answer without ordering anything.
func TestDeploymentWildcardWithoutCredentialsIsRefusedWithWhereToSaveThem(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	restore := UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported"))
	t.Cleanup(restore)
	previous := deploymentNSLookup
	deploymentNSLookup = nameservers(nil, nil)
	t.Cleanup(func() { deploymentNSLookup = previous })
	argv := fakeDNSCertbot(t, letsencrypt, "webroot", "dns-cloudflare")
	service := New(root, filepath.Join(root, "Caddyfile"))

	_, err := service.EnsureDeploymentCertificate(context.Background(), []string{"*.preview.example.test"}, nil)
	if !errors.Is(err, ErrCertificateWildcard) || !strings.Contains(err.Error(), "no DNS provider has credentials saved") ||
		!strings.Contains(err.Error(), "/proxy/certificates") {
		t.Fatalf("refusal = %v", err)
	}
	if _, err := service.DeploymentDNSProvider(context.Background(), "*.preview.example.test"); !errors.Is(err, ErrCertificateWildcard) {
		t.Fatalf("preflight's read = %v, want the same refusal", err)
	}
	raw, _ := os.ReadFile(argv)
	if strings.Contains(string(raw), "certonly") {
		t.Fatalf("certbot was asked to order:\n%s", raw)
	}
}

// On a host whose deployments the Docker Caddy ingress serves, a wildcard is
// ordered exactly as on nginx — through certbot, over DNS-01, with the saved
// credentials — rather than refused: the ingress serves a copy of the lineage
// and never has to answer the challenge itself. The lineage is then reused
// for the wildcard and for a preview under it, and the preflight read names
// the provider rather than a refusal.
func TestDeploymentWildcardIsIssuedAndReusedOnTheDockerCaddyIngress(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	t.Setenv("JD_ACME_DIRECTORY", "")
	previous := deploymentNSLookup
	deploymentNSLookup = nameservers(map[string][]string{"example.test": {"ada.ns.cloudflare.com"}}, nil)
	t.Cleanup(func() { deploymentNSLookup = previous })
	argv := fakeDNSCertbot(t, letsencrypt, "webroot", "dns-cloudflare")
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "docker"), `#!/bin/sh
case "$1" in
ps) echo caddy1 ;;
inspect) cat <<'JSON'
[{"Id":"caddy1","Name":"/edge","State":{"Running":true},
  "Config":{"Cmd":["caddy","run","--config","/etc/caddy/Caddyfile","--adapter","caddyfile"]},
  "Mounts":[{"Type":"bind","Source":"/srv/edge/Caddyfile","Destination":"/etc/caddy/Caddyfile"},
            {"Type":"volume","Source":"edge-config","Destination":"/config","RW":true},
            {"Type":"volume","Source":"edge-data","Destination":"/data","RW":true}],
  "NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]}}}]
JSON
;;
*) exit 1 ;;
esac
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := WriteDNSCredentials("cloudflare", "dns_cloudflare_api_token = secret-zone-token"); err != nil {
		t.Fatal(err)
	}
	service := NewWithDockerIngress(root, filepath.Join(root, "Caddyfile"))
	if edge, err := service.dockerCaddy(context.Background()); err != nil || edge == nil || edge.Name != "edge" {
		t.Fatalf("the fixture's ingress was not found: %+v, %v", edge, err)
	}

	provider, err := service.DeploymentDNSProvider(context.Background(), "*.preview.example.test")
	if err != nil || provider.Name != "Cloudflare" {
		t.Fatalf("preflight's read on a Caddy host = %+v, %v; want the provider", provider, err)
	}
	result, err := service.EnsureDeploymentCertificate(context.Background(), []string{"*.preview.example.test"}, nil)
	if err != nil || result.Outcome != CertificateIssued || result.Method != "dns-cloudflare" || result.Name != "preview.example.test" ||
		result.CertPath != filepath.Join(letsencrypt, "live", "preview.example.test", "fullchain.pem") {
		t.Fatalf("Caddy host = %+v, %v; want the wildcard issued over DNS-01", result, err)
	}
	raw, _ := os.ReadFile(argv)
	if !strings.Contains(string(raw), "certonly --dns-cloudflare") || strings.Contains(string(raw), "secret-zone-token") {
		t.Fatalf("certbot ran:\n%s", raw)
	}
	for _, name := range []string{"*.preview.example.test", "pr-7.preview.example.test"} {
		again, err := service.EnsureDeploymentCertificate(context.Background(), []string{name}, nil)
		if err != nil || again.Outcome != CertificateReused || again.Name != "preview.example.test" {
			t.Fatalf("%s = %+v, %v; want the wildcard reused", name, again, err)
		}
	}
	raw, _ = os.ReadFile(argv)
	if strings.Count(string(raw), "certonly ") != 1 {
		t.Fatalf("certbot ordered again:\n%s", raw)
	}
}
