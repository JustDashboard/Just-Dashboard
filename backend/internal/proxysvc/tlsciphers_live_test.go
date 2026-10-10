package proxysvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The deep scan against a real nginx and the OpenSSL it was built with, which
// is what it will meet in production. Each server answers to "localhost", so
// the scan sends a name, as it would for a domain.

func liveVersions(d *DeepScan) map[string]VersionSuites {
	out := map[string]VersionSuites{}
	for _, v := range d.Versions {
		out[v.Name] = v
	}
	return out
}

func openSSLNames(v VersionSuites) string {
	names := []string{}
	for _, s := range v.Suites {
		names = append(names, s.OpenSSL)
	}
	return strings.Join(names, " ")
}

// HTTP/2 has its own directive only where the installed nginx supports it.
func requireLiveNginxHTTP2(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nginx"); err != nil {
		t.Skip("nginx is not installed")
	}
	if !liveNginxHasHTTP2Directive(t) {
		t.Skip("nginx does not support the http2 on directive")
	}
}

// liveNginxHasHTTP2Directive asks the installed nginx whether it takes
// `http2 on;`, which arrived in 1.25.1; an older build turns HTTP/2 on in the
// listen instead.
func liveNginxHasHTTP2Directive(t *testing.T) bool {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "nginx.conf")
	content := fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\nevents {}\nhttp { access_log off; server { listen 127.0.0.1:8080; http2 on; } }\n", root)
	if err := os.WriteFile(config, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("nginx", "-p", root, "-c", config, "-t").CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), `unknown directive "http2"`) {
			return false
		}
		t.Fatalf("check nginx HTTP/2 support: %v\n%s", err, output)
	}
	return true
}

func TestLiveDeepScanOfNginx(t *testing.T) {
	requireLiveNginxHTTP2(t)
	cert, _ := scanTestCert(t, 70, nil)
	addr := scanNginx(t, cert, 1, func(ports []int, certPath, keyPath string) string {
		return fmt.Sprintf(`    server {
        listen 127.0.0.1:%d ssl;
        server_name localhost;
        ssl_certificate %s;
        ssl_certificate_key %s;
        ssl_protocols TLSv1.2 TLSv1.3;
        ssl_ciphers ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES128-GCM-SHA256;
        ssl_prefer_server_ciphers on;
        ssl_ecdh_curve X25519:prime256v1;
        http2 on;
        return 200 "ok";
    }`, ports[0], certPath, keyPath)
	})[0]
	_, port := splitAddr(t, addr)
	d := DeepScanTLS(context.Background(), "localhost", port, &DeepSite{Name: "localhost", HTTP2: true})
	if !d.Reachable || d.Where != "here" {
		t.Fatalf("reachable=%v where=%q error=%q", d.Reachable, d.Where, d.Error)
	}
	versions := liveVersions(d)
	// The server's own order, both of them, and nothing else.
	if v := versions["TLS 1.2"]; v.Status != "accepted" || !v.Complete || v.Order != "server" ||
		openSSLNames(v) != "ECDHE-ECDSA-AES256-GCM-SHA384 ECDHE-ECDSA-AES128-GCM-SHA256" {
		t.Errorf("TLS 1.2 = %+v", v)
	}
	if v := versions["TLS 1.3"]; v.Status != "accepted" || len(v.Suites) != 3 {
		t.Errorf("TLS 1.3 = %+v", v)
	}
	for _, name := range []string{"TLS 1.1", "TLS 1.0", "SSL 3.0"} {
		if versions[name].Status != "refused" {
			t.Errorf("%s = %+v", name, versions[name])
		}
	}
	groups := map[string]string{}
	for _, g := range d.Groups {
		groups[g.Name] = g.Status
	}
	if groups["X25519"] != "accepted" || groups["P-256"] != "accepted" || groups["P-384"] != "refused" {
		t.Errorf("groups: %+v", d.Groups)
	}
	// An ssl_ecdh_curve without the hybrid refuses it, whatever OpenSSL has.
	if groups["X25519MLKEM768"] != "refused" || d.BrowserGroup != "X25519" || deepFinding(d, "tls.kex.no-pq") == nil {
		t.Errorf("post-quantum: %s, browser %q, findings %+v", groups["X25519MLKEM768"], d.BrowserGroup, d.Findings)
	}
	if d.ALPN == nil || d.ALPN.Negotiated != "h2" || deepFinding(d, "tls.alpn.h2-off") != nil {
		t.Errorf("ALPN = %+v", d.ALPN)
	}
	if len(d.Resumption) == 0 || d.Resumption[0].Version != "TLS 1.3" || d.Resumption[0].Status != "resumed" {
		t.Errorf("resumption: %+v", d.Resumption)
	}
	// One server on the port is its default: a client naming nothing gets
	// its certificate.
	if len(d.SNI) != 2 || d.SNI[0].Status != "certificate" || !d.SNI[0].SameAsNamed || deepFinding(d, "tls.sni.default-certificate") == nil {
		t.Errorf("SNI: %+v", d.SNI)
	}
}

// A default server with ssl_reject_handshake — the fix the finding gives —
// refuses a client that names no site, or one this server does not have.
func TestLiveNginxRejectingUnknownNamesLeaksNothing(t *testing.T) {
	cert, _ := scanTestCert(t, 71, nil)
	addr := scanNginx(t, cert, 1, func(ports []int, certPath, keyPath string) string {
		return fmt.Sprintf(`    server {
        listen 127.0.0.1:%[1]d ssl default_server;
        ssl_reject_handshake on;
    }
    server {
        listen 127.0.0.1:%[1]d ssl;
        server_name localhost;
        ssl_certificate %[2]s;
        ssl_certificate_key %[3]s;
        return 200 "ok";
    }`, ports[0], certPath, keyPath)
	})[0]
	_, port := splitAddr(t, addr)
	d := DeepScanTLS(context.Background(), "localhost", port, nil)
	if len(d.SNI) != 2 {
		t.Fatalf("SNI: %+v", d.SNI)
	}
	for _, p := range d.SNI {
		if p.Status != "refused" || !strings.Contains(p.Detail, "unrecognized name") {
			t.Errorf("%s: %+v", p.Kind, p)
		}
	}
	if deepFinding(d, "tls.sni.default-certificate") != nil {
		t.Errorf("findings: %+v", d.Findings)
	}
	if liveVersions(d)["TLS 1.3"].Status != "accepted" {
		t.Errorf("the named site should still answer: %+v", d.Versions)
	}
}

// RSA key exchange and nothing else, in the client's order: no forward
// secrecy anywhere, and a client can pick the weaker suite.
func TestLiveNginxWithOnlyRSAKeyExchangeHasNoForwardSecrecy(t *testing.T) {
	addr := scanNginx(t, rsaTestCert(t), 1, func(ports []int, certPath, keyPath string) string {
		return fmt.Sprintf(`    server {
        listen 127.0.0.1:%d ssl;
        server_name localhost;
        ssl_certificate %s;
        ssl_certificate_key %s;
        ssl_protocols TLSv1.2;
        ssl_ciphers AES128-GCM-SHA256:AES256-SHA;
        return 200 "ok";
    }`, ports[0], certPath, keyPath)
	})[0]
	_, port := splitAddr(t, addr)
	d := DeepScanTLS(context.Background(), "localhost", port, nil)
	v := liveVersions(d)["TLS 1.2"]
	if v.Status != "accepted" || v.Order != "client" || openSSLNames(v) != "AES128-GCM-SHA256 AES256-SHA" {
		t.Fatalf("TLS 1.2 = %+v", v)
	}
	for _, id := range []string{"tls.cipher.no-forward-secrecy", "tls.cipher.weak", "tls.cipher.client-order.TLS 1.2", "tls.no-13"} {
		if deepFinding(d, id) == nil {
			t.Errorf("%s missing: %+v", id, d.Findings)
		}
	}
	if len(d.Groups) != 0 || d.BrowserGroup != "" {
		t.Errorf("no ECDHE and no TLS 1.3, so no group to ask about: %+v %q", d.Groups, d.BrowserGroup)
	}
}

// A DHE suite's group size is read from its key exchange: 1024 bits is
// Logjam's range.
func TestLiveNginxSmallDHGroup(t *testing.T) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl is not installed")
	}
	params := filepath.Join(t.TempDir(), "dh1024.pem")
	if out, err := exec.Command(openssl, "dhparam", "-dsaparam", "-out", params, "1024").CombinedOutput(); err != nil {
		t.Fatalf("dhparam: %v %s", err, out)
	}
	addr := scanNginx(t, rsaTestCert(t), 1, func(ports []int, certPath, keyPath string) string {
		return fmt.Sprintf(`    server {
        listen 127.0.0.1:%d ssl;
        server_name localhost;
        ssl_certificate %s;
        ssl_certificate_key %s;
        ssl_protocols TLSv1.2;
        ssl_ciphers DHE-RSA-AES128-GCM-SHA256:@SECLEVEL=0;
        ssl_dhparam %s;
        return 200 "ok";
    }`, ports[0], certPath, keyPath, params)
	})[0]
	_, port := splitAddr(t, addr)
	d := DeepScanTLS(context.Background(), "localhost", port, nil)
	if d.DHBits != 1024 || deepFinding(d, "tls.kex.weak-dh") == nil {
		t.Fatalf("dh bits %d, findings %+v, versions %+v", d.DHBits, d.Findings, d.Versions)
	}
}
