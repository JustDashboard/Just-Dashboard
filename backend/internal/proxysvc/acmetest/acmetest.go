// Package acmetest runs a certificate authority that really validates DNS-01,
// for live tests: Pebble, Let's Encrypt's test server, asking a BIND that
// accepts RFC 2136 updates signed with a key the test holds. certbot's
// dns-rfc2136 plugin — one of the providers the Certificates page drives —
// writes the challenge record there, and Pebble reads it back before it signs,
// so a certificate from here was proved the way a public one is.
package acmetest

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// DNS01 is a running authority and the nameserver it validates against.
type DNS01 struct {
	// Directory is Pebble's ACME directory, for JD_ACME_DIRECTORY.
	Directory string
	// Bundle is the root behind Pebble's own HTTPS listener, for certbot's
	// REQUESTS_CA_BUNDLE.
	Bundle string
	// Credentials is what the Certificates page saves for the RFC 2136
	// provider to update Zone on this BIND.
	Credentials string
	Zone        string

	management string
	client     *http.Client
}

// StartDNS01 starts BIND, authoritative for zone and open to signed updates
// of its TXT records, and a Pebble on the same Docker network that resolves
// challenges through it. Everything is removed when the test ends. It skips
// the test where Docker is not available.
func StartDNS01(ctx context.Context, t testing.TB, zone string) *DNS01 {
	t.Helper()
	if !hostexec.Available("docker") {
		t.Skip("docker is not installed here")
	}
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	remove := func(args ...string) { _ = hostexec.Command(context.Background(), "docker", args...).Run() }
	prefix := fmt.Sprintf("jdtest-dns01-%d", time.Now().UnixNano())
	dir := t.TempDir()

	network := prefix + "-net"
	docker("network", "create", network)
	t.Cleanup(func() { remove("network", "rm", network) })

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(secret)
	files := map[string]string{
		"named.conf": `options {
  directory "/var/cache/bind";
  listen-on port 53 { any; };
  listen-on-v6 { none; };
  allow-query { any; };
  recursion no;
  dnssec-validation no;
  notify no;
};
key "jd-live-key." { algorithm hmac-sha256; secret "` + key + `"; };
zone "` + zone + `" {
  type primary;
  file "/var/lib/bind/zone.db";
  update-policy { grant jd-live-key. zonesub TXT; };
};
`,
		"zone.db": "$TTL 30\n@ IN SOA ns." + zone + ". hostmaster." + zone + ". ( 1 60 60 600 30 )\n" +
			"@ IN NS ns." + zone + ".\nns IN A 127.0.0.1\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bind := prefix + "-bind"
	docker("create", "--name", bind, "--network", network, "-p", "127.0.0.1::53/tcp",
		"internetsystemsconsortium/bind9:9.20", "-g", "-c", "/etc/bind/named.conf")
	t.Cleanup(func() { remove("rm", "-f", "-v", bind) })
	docker("cp", filepath.Join(dir, "named.conf"), bind+":/etc/bind/named.conf")
	docker("cp", filepath.Join(dir, "zone.db"), bind+":/var/lib/bind/zone.db")
	docker("start", bind)
	address := docker("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", bind)
	_, port, _ := strings.Cut(docker("port", bind, "53/tcp"), "127.0.0.1:")

	// Real validation, so no PEBBLE_VA_ALWAYS_VALID; no random sleeps and no
	// refused nonces, which are Pebble testing the client rather than this.
	config := filepath.Join(dir, "pebble-config.json")
	if err := os.WriteFile(config, []byte(`{"pebble": {
  "listenAddress": "0.0.0.0:14000", "managementListenAddress": "0.0.0.0:15000",
  "certificate": "test/certs/localhost/cert.pem", "privateKey": "test/certs/localhost/key.pem",
  "httpPort": 5002, "tlsPort": 5001, "ocspResponderURL": "", "externalAccountBindingRequired": false,
  "keyAlgorithm": "ecdsa", "retryAfter": {"authz": 3, "order": 5},
  "profiles": {"default": {"description": "ninety days", "validityPeriod": 7776000}}
}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pebble := docker("run", "-d", "--name", prefix+"-pebble", "--network", network,
		"-e", "PEBBLE_VA_NOSLEEP=1", "-e", "PEBBLE_WFE_NONCEREJECT=0",
		"-v", config+":/test/config/pebble-config.json:ro",
		"-p", "127.0.0.1::14000", "-p", "127.0.0.1::15000",
		"ghcr.io/letsencrypt/pebble:latest", "-config", "/test/config/pebble-config.json", "-dnsserver", address+":53")
	t.Cleanup(func() {
		if t.Failed() {
			raw, _ := hostexec.Command(context.Background(), "docker", "logs", "--tail", "40", pebble).CombinedOutput()
			t.Logf("pebble:\n%s", raw)
		}
		remove("rm", "-f", "-v", pebble)
	})

	fixture := &DNS01{
		Directory: "https://" + docker("port", pebble, "14000/tcp") + "/dir",
		Bundle:    filepath.Join(dir, "minica.pem"),
		Credentials: "dns_rfc2136_server = 127.0.0.1\ndns_rfc2136_port = " + port + "\n" +
			"dns_rfc2136_name = jd-live-key.\ndns_rfc2136_secret = " + key + "\ndns_rfc2136_algorithm = HMAC-SHA256\n",
		Zone:       zone,
		management: "https://" + docker("port", pebble, "15000/tcp"),
	}
	for attempt := 0; ; attempt++ {
		if err := hostexec.Command(ctx, "docker", "cp", pebble+":/test/certs/pebble.minica.pem", fixture.Bundle).Run(); err == nil {
			if raw, _ := os.ReadFile(fixture.Bundle); strings.Contains(string(raw), "BEGIN CERTIFICATE") {
				break
			}
		}
		if attempt > 40 {
			t.Fatalf("Pebble's listener root was not readable: %s", docker("logs", pebble))
		}
		time.Sleep(250 * time.Millisecond)
	}
	listener := x509.NewCertPool()
	raw, _ := os.ReadFile(fixture.Bundle)
	listener.AppendCertsFromPEM(raw)
	fixture.client = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: listener}}}
	// Both answer before they are ready; certbot's first request should not
	// be the one that finds out.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(250 * time.Millisecond) {
		response, err := fixture.client.Get(fixture.Directory)
		if err == nil {
			response.Body.Close()
		}
		soa := hostexec.Command(ctx, "docker", "exec", bind, "dig", "+short", "@127.0.0.1", zone, "SOA").Run()
		if err == nil && response.StatusCode == http.StatusOK && soa == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Pebble (%v) or BIND (%v) did not come up: %s", err, soa, docker("logs", bind))
		}
	}
	return fixture
}

// Roots is the root Pebble signs certificates with: a new one every start,
// so a certificate that chains to it was signed by this run's Pebble.
func (f *DNS01) Roots(t testing.TB) *x509.CertPool {
	t.Helper()
	response, err := f.client.Get(f.management + "/roots/0")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		t.Fatalf("Pebble's issuing root is not PEM: %s", raw)
	}
	return pool
}
