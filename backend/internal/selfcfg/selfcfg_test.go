package selfcfg

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The comments in this file are the operator's, written by install.sh and
// edited over years. A settings page that ate them the first time somebody
// changed a port would be a bad trade for the convenience.
func TestSaveKeepsCommentsAndOrder(t *testing.T) {
	path := writeEnv(t, `# Written by install.sh.
JD_MASTER_KEY=abc

# The port you connect to.
JD_PORT=8443
JD_FRONTEND_PORT=3000
`)
	env, err := LoadEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	env.Set("JD_PORT", "9443")
	env.Set("JD_REQUIRE_2FA", "true")
	if _, err := env.Save(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		"# Written by install.sh.",
		"# The port you connect to.\nJD_PORT=9443",
		"JD_MASTER_KEY=abc",
		"JD_REQUIRE_2FA=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("saved file lost %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "JD_PORT=") != 1 {
		t.Fatalf("JD_PORT was appended rather than replaced:\n%s", got)
	}
}

// The backup is what the rollback restores, so a save that did not take one
// would quietly remove the safety net this whole package is built around.
func TestSaveTakesABackup(t *testing.T) {
	path := writeEnv(t, "JD_PORT=8443\n")
	env, err := LoadEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	env.Set("JD_PORT", "9443")
	backup, err := env.Save()
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("no backup path returned")
	}
	b, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "JD_PORT=8443") {
		t.Fatalf("backup does not hold the previous value: %s", b)
	}
}

func TestReadSettingsFillsGapsWithTheShippedDefaults(t *testing.T) {
	path := writeEnv(t, "JD_SITE=box.tail1234.ts.net\nJD_TLS=tailscale\nJD_PORT=8443\n")
	env, err := LoadEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	s := ReadSettings(env)
	if s.Site != "box.tail1234.ts.net" || s.TLS != TLSTailscale {
		t.Fatalf("site/tls not read: %+v", s)
	}
	if s.BackendPort != 8080 || s.FrontendPort != 3000 {
		t.Fatalf("absent ports should read as the compose defaults: %+v", s)
	}
	if s.Endpoint() != "https://box.tail1234.ts.net:8443" {
		t.Fatalf("endpoint = %q", s.Endpoint())
	}
}

func TestEndpointIsPlainHTTPOnlyWhenTLSIsOff(t *testing.T) {
	s := Settings{Site: "localhost", TLS: TLSOff, Port: 8443}
	if s.Endpoint() != "http://localhost:8443" {
		t.Fatalf("endpoint = %q", s.Endpoint())
	}
}

// The rule this package exists to enforce: the allowlist is checked before
// authentication, so an operator who drops their own network out of it does
// not get an error, they get a dashboard that has stopped existing for them.
func TestValidateRefusesAnAllowlistThatLocksTheCallerOut(t *testing.T) {
	old := defaults()
	next := old
	next.AllowedCIDRs = "127.0.0.1/32,10.0.0.0/8"
	err := next.Validate(old, "100.101.102.103", nil)
	if err == nil || !strings.Contains(err.Error(), "100.101.102.103") {
		t.Fatalf("validate = %v, want a refusal naming the caller's address", err)
	}
}

func TestValidateInsistsOnLoopback(t *testing.T) {
	old := defaults()
	next := old
	next.AllowedCIDRs = "100.64.0.0/10"
	if err := next.Validate(old, "100.64.0.1", nil); err == nil {
		t.Fatal("an allowlist without loopback was accepted; the SSH tunnel is the way back in")
	}
}

func TestValidateRefusesPlainHTTPOffLoopback(t *testing.T) {
	old := defaults()
	next := old
	next.Site = "100.101.102.103"
	next.TLS = TLSOff
	if err := next.Validate(old, "127.0.0.1", nil); err == nil {
		t.Fatal("plain HTTP was accepted on a routable address")
	}
}

func TestValidateRefusesATailscaleCertificateForAnIP(t *testing.T) {
	old := defaults()
	next := old
	next.Site = "100.101.102.103"
	next.TLS = TLSTailscale
	if err := next.Validate(old, "127.0.0.1", nil); err == nil {
		t.Fatal("a Tailscale certificate was accepted for an address it can never be issued for")
	}
}

func TestValidateSeparatesCollidingPorts(t *testing.T) {
	old := defaults()
	next := old
	next.FrontendPort = next.BackendPort
	if err := next.Validate(old, "127.0.0.1", nil); err != nil {
		t.Fatal(err)
	}
	if next.FrontendPort == next.BackendPort || next.FrontendPort == next.Port {
		t.Fatal("two services were allowed to share one port")
	}
}

// Only a port that is moving is probed, because the three it is on now are
// held by this very stack. Testing those would report the dashboard as
// colliding with itself.
func TestValidateOnlyProbesPortsThatMove(t *testing.T) {
	old := defaults()
	next := old
	next.FrontendPort = 20001

	probed := []int{}
	free := func(port int) error {
		probed = append(probed, port)
		return nil
	}
	if err := next.Validate(old, "127.0.0.1", free); err != nil {
		t.Fatal(err)
	}
	if len(probed) != 1 || probed[0] != 20001 {
		t.Fatalf("probed %v, want only the port that changed", probed)
	}
}

func TestDiffNamesOnlyWhatMoved(t *testing.T) {
	old := defaults()
	next := old
	next.Port = 9443
	next.Require2FA = true

	changes := Diff(old, next)
	if len(changes) != 2 {
		t.Fatalf("diff = %+v, want two entries", changes)
	}
}

// The process reports its durations as time.Duration renders them ("12h0m0s")
// while the file holds what the operator typed ("12h"). That is the same
// setting, and a warning that .env has drifted on every page load because of
// it would teach operators to ignore the warning.
func TestDriftIgnoresHowADurationIsSpelled(t *testing.T) {
	onDisk := defaults()
	onDisk.SessionTTL = "12h"
	onDisk.IdleTTL = "60m"
	onDisk.AllowedCIDRs = "127.0.0.1/8"
	s := &Service{observed: func() Observed {
		return Observed{
			Site:            onDisk.Site,
			TLS:             onDisk.TLS,
			BackendPort:     onDisk.BackendPort,
			AllowedCIDRs:    onDisk.AllowedCIDRs,
			TerminalEnabled: onDisk.TerminalEnabled,
			Require2FA:      onDisk.Require2FA,
			SessionTTL:      (12 * time.Hour).String(),
			IdleTTL:         (60 * time.Minute).String(),
			UpdateCheck:     onDisk.UpdateCheck,
		}
	}}
	if drift := s.drift(onDisk); len(drift) != 0 {
		t.Fatalf("drift = %+v, want none", drift)
	}

	onDisk.IdleTTL = "45m"
	drift := s.drift(onDisk)
	if len(drift) != 1 || drift[0].Key != keyIdleTTL || drift[0].From != "1h0m0s" || drift[0].To != "45m0s" {
		t.Fatalf("drift = %+v, want only the idle timeout, from 1h0m0s to 45m0s", drift)
	}
}

// Every way this feature can fail in a way an operator cannot recover from is
// a wrong flag here, and this is the only check on it that needs no Docker.
func TestSiblingArgv(t *testing.T) {
	a := NewApplier(NewStore("/var/lib/just-dashboard"), "/var/lib/just-dashboard",
		"unix:///var/run/docker.sock", slog.New(slog.NewTextHandler(io.Discard, nil)))
	args := a.siblingArgs(&Run{
		Dir:   "/opt/Just-Dashboard",
		Image: "just-dashboard-backend:latest",
	})
	line := strings.Join(args, " ")
	for _, want := range []string{
		"run --detach",
		"--name " + RestartContainer,
		"--network host",
		"--restart no",
		"-v /var/run/docker.sock:/var/run/docker.sock",
		"-v /opt/Just-Dashboard:/opt/Just-Dashboard",
		"-v /var/lib/just-dashboard:/var/lib/just-dashboard",
		"-w /opt/Just-Dashboard",
		"--entrypoint /usr/local/bin/just-dashboard",
		"just-dashboard-backend:latest -self-restart -state-dir /var/lib/just-dashboard",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("argv is missing %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "JD_MASTER_KEY") {
		t.Fatal("the sibling was handed a secret it has no use for")
	}
}

// The shape of a real `tailscale status --json`, trimmed to the fields this
// reads. Taken from a machine on a tailnet with HTTPS switched off, which is
// the state that used to surface as a settings form rejecting its own
// suggestion.
const tailscaleStatusFixture = `{
  "Version": "1.80.0",
  "BackendState": "Running",
  "MagicDNSSuffix": "tailed39ba.ts.net",
  "CertDomains": null,
  "Self": {
    "HostName": "vps-07749119-vps-ovh-net",
    "DNSName": "vps-07749119-vps-ovh-net.tailed39ba.ts.net.",
    "TailscaleIPs": ["100.110.34.31", "fd7a:115c:a1e0::9e37:2220"]
  }
}`

func TestParseTailscaleStatus(t *testing.T) {
	id := parseTailscaleStatus([]byte(tailscaleStatusFixture))
	if !id.Available || !id.Running {
		t.Fatalf("a running client read as available:%v running:%v", id.Available, id.Running)
	}
	if id.Hostname != "vps-07749119-vps-ovh-net.tailed39ba.ts.net" {
		t.Fatalf("hostname = %q; the trailing dot belongs to DNS, not to a browser", id.Hostname)
	}
	// The IPv6 address is a perfectly good tailnet address and a useless bind
	// for this purpose: the proxy is given one address and it has to be the v4.
	if id.IP4 != "100.110.34.31" {
		t.Fatalf("ip4 = %q", id.IP4)
	}
	if id.HTTPSEnabled {
		t.Fatal("a tailnet with no CertDomains was reported as able to issue certificates")
	}
	if !strings.Contains(id.Detail, "login.tailscale.com/admin/dns") {
		t.Fatalf("detail does not say where the switch is: %q", id.Detail)
	}
	if !id.Usable() {
		t.Fatal("a running client with a MagicDNS name should still be usable — only the padlock is missing")
	}
}

func TestParseTailscaleStatusWhenLoggedOut(t *testing.T) {
	id := parseTailscaleStatus([]byte(`{"BackendState":"NeedsLogin","Self":{}}`))
	if id.Running || id.Usable() {
		t.Fatal("a logged-out client was reported as usable")
	}
	if !strings.Contains(id.Detail, "tailscale up") {
		t.Fatalf("detail does not say what to run: %q", id.Detail)
	}
}

// Adding rather than replacing: the entries already there are the operator's,
// and picking a certificate has no business deciding they were wrong.
func TestWithTailnetAddsTheRangeOnceOnly(t *testing.T) {
	got := WithTailnet("127.0.0.1/32,::1/128")
	if got != "100.64.0.0/10,127.0.0.1/32,::1/128" {
		t.Fatalf("WithTailnet = %q", got)
	}
	if again := WithTailnet(got); again != got {
		t.Fatalf("WithTailnet is not idempotent: %q", again)
	}
	if covered := WithTailnet("100.64.0.0/10,127.0.0.1/32"); covered != "100.64.0.0/10,127.0.0.1/32" {
		t.Fatalf("an allowlist that already covers the tailnet was rewritten: %q", covered)
	}
}

// writeSelfSignedCert puts a certificate for name where the proxy looks for
// one, which is all Certificate and DropStaleCertificate read.
func writeSelfSignedCert(t *testing.T, dataDir, name string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, "certs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(mustCreate(t, filepath.Join(dir, CertFile)), &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte("key"), 0o640); err != nil {
		t.Fatal(err)
	}
}

func mustCreate(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// The proxy chooses between a real certificate and its internal CA by whether
// the files exist, so a certificate left over from the address before this one
// would be served under the new name — a browser error about the wrong host,
// which is a worse outcome than the self-signed warning it replaced.
func TestDropStaleCertificateKeepsTheOneThatFits(t *testing.T) {
	dir := t.TempDir()
	writeSelfSignedCert(t, dir, "box.tailnet.ts.net")

	if state := Certificate(dir, "box.tailnet.ts.net"); !state.Issued || state.Expires == nil {
		t.Fatalf("certificate for the configured name read as %+v, want issued", state)
	}
	DropStaleCertificate(dir, "box.tailnet.ts.net")
	if !Certificate(dir, "box.tailnet.ts.net").Issued {
		t.Fatal("a certificate covering the address was deleted")
	}

	if Certificate(dir, "other.tailnet.ts.net").Issued {
		t.Fatal("a certificate for another name was reported as covering this one")
	}
	DropStaleCertificate(dir, "other.tailnet.ts.net")
	if _, err := os.Stat(filepath.Join(dir, "certs", CertFile)); !os.IsNotExist(err) {
		t.Fatalf("stale certificate still on disk (%v), so the proxy would serve the wrong name", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "certs", KeyFile)); !os.IsNotExist(err) {
		t.Fatal("the key outlived the certificate it belongs to")
	}
}
