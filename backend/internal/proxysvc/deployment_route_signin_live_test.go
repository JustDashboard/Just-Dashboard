package proxysvc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// What a real Caddy and a real Authelia do with a deployment route that
// signs in, with the auth server published on loopback the way a container
// on this host is: Caddy is joined to its network and asks it at its
// address there. A visitor without a session is sent to Authelia's sign-in
// page; after signing in they reach the application as the user Authelia
// named and never as the one they claimed; the redirecting name answers
// without asking; a Caddy that lost the auth server's network has it back
// after one maintenance pass; and with Authelia gone the route refuses
// everyone rather than letting them through.
func TestLiveDockerCaddyGatesRouteBehindForwardAuth(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker ingress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker fixture command failed: %v: %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	remove := func(args ...string) { _ = hostexec.Command(context.Background(), "docker", args...).Run() }
	prefix := fmt.Sprintf("jdtest-sso-%d", time.Now().UnixNano())
	dir := t.TempDir()

	// A private authority for the route and the sign-in page, so the client
	// below checks the certificates Caddy serves rather than ignoring them.
	authority, certPath, keyPath := signInFixtureCertificates(t, dir, "app.sso.test", "www.app.sso.test", "auth.sso.test")
	const password = "correct horse battery"
	authelia := signInFixtureAuthelia(t, dir, password)

	ingress, authNetwork := prefix+"-ingress", prefix+"-auth"
	docker("network", "create", ingress)
	defer remove("network", "rm", ingress)
	docker("network", "create", authNetwork)
	defer remove("network", "rm", authNetwork)
	autheliaName := prefix + "-authelia"
	docker("run", "-d", "--name", autheliaName, "--network", authNetwork, "-p", "127.0.0.1::9091",
		"-v", authelia+":/config", "authelia/authelia:4.39")
	defer remove("rm", "-f", "-v", autheliaName)
	app := docker("run", "-d", "--name", prefix+"-app", "-p", "127.0.0.1::80", "traefik/whoami:v1.11")
	defer remove("rm", "-f", "-v", app)

	// The sign-in page is a site of the operator's own, as it would be on a
	// real host, reached by Authelia's name once activation has joined Caddy
	// to Authelia's network.
	source := filepath.Join(dir, "Caddyfile")
	fixture := "auth.sso.test {\n  tls /fixture/route.crt /fixture/route.key\n  reverse_proxy " + autheliaName + ":9091\n}\n"
	if err := os.WriteFile(source, []byte(fixture), 0644); err != nil {
		t.Fatal(err)
	}
	// A fixed host port: joining Caddy to the default bridge for the
	// application has Docker publish its ports again, and an ephemeral one
	// would move.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	id := docker("run", "-d", "--name", prefix+"-caddy", "--network", ingress, "-p", fmt.Sprintf("127.0.0.1:%d:443", httpsPort),
		"-v", source+":/etc/caddy/Caddyfile:ro", "-v", dir+":/fixture:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer remove("rm", "-f", "-v", id)
	published := func(id, port string) string {
		_, value, _ := strings.Cut(docker("port", id, port+"/tcp"), "127.0.0.1:")
		return value
	}
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-persistent-storage"}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	pool := x509.NewCertPool()
	pool.AddCert(authority)
	endpoint := fmt.Sprintf("127.0.0.1:%d", httpsPort)
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, endpoint)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request := func(method, address, body string, header ...string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, address, strings.NewReader(body))
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v; ingress log: %s", method, address, err, docker("logs", "--tail", "20", id))
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res, string(raw)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(time.Second) {
		res, err := http.Get("http://127.0.0.1:" + published(autheliaName, "9091") + "/api/health")
		if err == nil {
			res.Body.Close()
		}
		if err == nil && res.StatusCode == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Authelia did not start: %v; log: %s", err, docker("logs", autheliaName))
		}
	}

	service := New(dir, source)
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.sso.test"},
		Upstream: "http://127.0.0.1:" + published(app, "80"), TLS: true, ForceHTTPS: true,
		CertPath: certPath, KeyPath: keyPath,
		Redirects: []HostRedirect{{From: "www.app.sso.test", To: "app.sso.test"}},
		ForwardAuth: &SiteForwardAuth{
			Provider: "authelia", SignIn: "https://auth.sso.test/",
			Verify: "http://127.0.0.1:" + published(autheliaName, "9091") + "/api/authz/auth-request",
		},
	}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}
	// What activation verifies afterwards is what was written: the auth
	// server's container address, not this host's loopback, and its target.
	want, _, err := edge.routeContent(ctx, route)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, err := edge.snapshot(ctx, route.Name); err != nil || snapshot.Content != want ||
		strings.Contains(want, "127.0.0.1") || !strings.Contains(want, dockerCaddyAuthTargetPrefix) {
		t.Fatalf("written route = %v:\n%s", err, snapshot.Content)
	}

	res, _ := request("GET", "https://app.sso.test/hello?x=1", "", "Remote-User", "mallory")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "https://auth.sso.test/?rd=https://app.sso.test/hello?x=1" {
		t.Fatalf("a visitor without a session = %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, body := request("POST", "https://auth.sso.test/api/firstfactor",
		`{"username":"alice","password":"`+password+`","keepMeLoggedIn":false,"targetURL":"https://app.sso.test/hello?x=1"}`,
		"Content-Type", "application/json")
	var session string
	for _, cookie := range res.Cookies() {
		if cookie.Name == "authelia_session" {
			session = cookie.Value
		}
	}
	if res.StatusCode != http.StatusOK || session == "" {
		t.Fatalf("signing in at Authelia = %d %s", res.StatusCode, body)
	}
	signedIn := func() (*http.Response, string) {
		return request("GET", "https://app.sso.test/hello", "", "Remote-User", "mallory", "Remote-Email", "mallory@example.test",
			"Cookie", "authelia_session="+session)
	}
	res, body = signedIn()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Remote-User: alice") ||
		!strings.Contains(body, "Remote-Email: alice@sso.test") || strings.Contains(body, "mallory") {
		t.Fatalf("a signed-in visitor = %d:\n%s", res.StatusCode, body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Remote-") || strings.HasPrefix(line, "Host:") {
			t.Logf("the application was told %s", strings.TrimSpace(line))
		}
	}
	if res, _ := request("GET", "https://www.app.sso.test/old", ""); res.StatusCode != http.StatusPermanentRedirect ||
		res.Header.Get("Location") != "https://app.sso.test/old" {
		t.Fatalf("the redirecting name = %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	// A Caddy recreated, or disconnected by hand, without the auth server's
	// network refuses everyone; the maintenance pass joins it again.
	docker("network", "disconnect", authNetwork, id)
	if res, _ := signedIn(); res.StatusCode == http.StatusOK {
		t.Fatal("the route answered without being able to ask the auth server")
	}
	repaired := map[string]bool{}
	if _, err := edge.reconcile(ctx, func(name string, ok bool) { repaired[name] = ok }); err != nil || !repaired[route.Name] {
		t.Fatalf("maintenance = %v %v", err, repaired)
	}
	if res, body := signedIn(); res.StatusCode != http.StatusOK || !strings.Contains(body, "Remote-User: alice") {
		t.Fatalf("after the maintenance pass = %d:\n%s", res.StatusCode, body)
	}

	// No auth server, no application: the check failing is a refusal.
	docker("stop", autheliaName)
	if res, body := signedIn(); res.StatusCode == http.StatusOK || strings.Contains(body, "Remote-User") {
		t.Fatalf("with Authelia stopped = %d:\n%s", res.StatusCode, body)
	}
}

// signInFixtureCertificates writes a private authority's certificate for
// the names, as the files an imported certificate would be, and returns the
// authority to trust.
func signInFixtureCertificates(t *testing.T, dir string, names ...string) (*x509.Certificate, string, string) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Just Dashboard sign-in fixture"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	authority, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, authority, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPath, keyPath := filepath.Join(dir, "route.crt"), filepath.Join(dir, "route.key")
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	if err := os.WriteFile(certPath, chain, 0644); err != nil {
		t.Fatal(err)
	}
	// Readable by Caddy's user through the fixture mount, and nothing else
	// on a throwaway directory.
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0644); err != nil {
		t.Fatal(err)
	}
	return authority, certPath, keyPath
}

// signInFixtureAuthelia writes an Authelia configuration with one user,
// alice, who signs in with one factor to anything under sso.test.
func signInFixtureAuthelia(t *testing.T, dir, password string) string {
	t.Helper()
	root := filepath.Join(dir, "authelia")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	secret := func() string {
		raw := make([]byte, 32)
		_, _ = rand.Read(raw)
		return fmt.Sprintf("%x", raw)
	}
	configuration := fmt.Sprintf(`server:
  address: 'tcp://:9091'
identity_validation:
  reset_password:
    jwt_secret: '%s'
authentication_backend:
  file:
    path: '/config/users.yml'
access_control:
  default_policy: 'one_factor'
session:
  secret: '%s'
  cookies:
    - domain: 'sso.test'
      authelia_url: 'https://auth.sso.test'
storage:
  encryption_key: '%s'
  local:
    path: '/config/db.sqlite3'
notifier:
  filesystem:
    filename: '/config/notification.txt'
`, secret(), secret(), secret())
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var users bytes.Buffer
	fmt.Fprintf(&users, "users:\n  alice:\n    displayname: 'Alice'\n    password: '%s'\n    email: 'alice@sso.test'\n    groups: []\n", hash)
	for name, content := range map[string][]byte{"configuration.yml": []byte(configuration), "users.yml": users.Bytes()} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
