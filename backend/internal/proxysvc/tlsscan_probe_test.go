package proxysvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// scanTestCert is a leaf for a local listener: its serial and OCSP responder
// are what the report is being tested on.
func scanTestCert(t *testing.T, serial int64, ocsp []string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "scan.test"},
		DNSNames:     []string{"scan.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		OCSPServer:   ocsp,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, parsed
}

// rsaTestCert is an RSA leaf for scan.test, which RSA key exchange needs.
func rsaTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(8), Subject: pkix.Name{CommonName: "scan.test"},
		DNSNames: []string{"scan.test"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// splitAddr is a listener's address as ScanTLS takes it.
func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

// tlsListener serves TLS on 127.0.0.1 and hands each completed handshake to
// serve; it never speaks HTTP unless serve does.
func tlsListener(t *testing.T, config *tls.Config, serve func(*tls.Conn)) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				tc := conn.(*tls.Conn)
				tc.SetDeadline(time.Now().Add(5 * time.Second))
				if tc.Handshake() == nil {
					serve(tc)
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// The server's protocol_version alert reads "protocol version not supported",
// and matching those words once filed every correct refusal of TLS 1.0 and
// 1.1 as this client's own — a well-configured server could never be shown
// refusing them. Probed against a listener that really refuses.
func TestProbeProtocolsReportsTheServersRefusal(t *testing.T) {
	cert, _ := scanTestCert(t, 1, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		func(*tls.Conn) {})

	got := map[string]ProtocolResult{}
	for _, p := range probeProtocols(context.Background(), addr, "scan.test", "") {
		got[p.Name] = p
	}
	for name, want := range map[string]string{
		"TLS 1.0": "refused", "TLS 1.1": "refused", "TLS 1.2": "offered", "TLS 1.3": "offered",
	} {
		if got[name].Status != want {
			t.Errorf("%s = %+v, want %s", name, got[name], want)
		}
	}
	if !strings.Contains(got["TLS 1.0"].Detail, "protocol version not supported") {
		t.Errorf("the refusal should carry the server's own words: %q", got["TLS 1.0"].Detail)
	}
}

func TestProbeProtocolsFindsAMissingTLS13(t *testing.T) {
	cert, _ := scanTestCert(t, 1, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert},
		MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}, func(*tls.Conn) {})

	scan := goodScan()
	scan.Protocols = probeProtocols(context.Background(), addr, "scan.test", "")
	if status := protocolStatus(scan.Protocols, "TLS 1.3"); status != "refused" {
		t.Fatalf("TLS 1.3 = %s: %+v", status, scan.Protocols)
	}
	grade(scan)
	if !hasFinding(scan, "tls.no-13") {
		t.Fatalf("no finding for the missing TLS 1.3: %+v", scan.Findings)
	}
}

// Only the server's answer is a refusal. Anything that could be the network
// or this client stays unknown, because "refused" there is false reassurance.
func TestProtocolAnswer(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		status string
		detail string
	}{
		{"an alert", &net.OpError{Op: "remote error", Err: errString("tls: protocol version not supported")},
			"refused", "The server answered: protocol version not supported."},
		// OpenSSL's answer when the version is fine and no cipher is shared,
		// and Java 8's refusal: it cannot be told which.
		{"a handshake failure alert", &net.OpError{Op: "remote error", Err: errString("tls: handshake failure")},
			"unknown", "or accepts it only with a cipher this probe cannot offer"},
		{"an insufficient security alert", &net.OpError{Op: "remote error", Err: errString("tls: insufficient security")},
			"unknown", "The server answered: insufficient security."},
		{"a close on the hello", io.EOF, "refused", "closed the connection"},
		{"a reset on the hello", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, "refused", "closed the connection"},
		{"another version picked", errString("tls: server selected unsupported protocol version 303"),
			"refused", "different version"},
		{"this client would not ask", errString("tls: no supported versions satisfy MinVersion and MaxVersion"),
			"unknown", "never asked"},
		{"no connection", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "unknown", "could not connect"},
		{"no answer in time", context.DeadlineExceeded, "unknown", "did not answer in time"},
		{"something else", errString("tls: unexpected message"), "unknown", "unexpected message"},
	} {
		status, detail := protocolAnswer(c.err)
		if status != c.status || !strings.Contains(detail, c.detail) {
			t.Errorf("%s: got %s %q, want %s containing %q", c.name, status, detail, c.status, c.detail)
		}
	}
}

// openssl, browsers and crt.sh all print the serial in hex; the decimal form
// matched none of them.
func TestDescribeChainPrintsTheSerialInHex(t *testing.T) {
	_, leaf := scanTestCert(t, 0x04D351, nil)
	scan := &TLSScan{Chain: []ChainLink{}}
	describeChain(scan, []*x509.Certificate{leaf}, "scan.test")
	if scan.Serial != "04:D3:51" {
		t.Fatalf("serial = %q", scan.Serial)
	}
	if scan.Serial != scan.Certificate.Serial {
		t.Fatalf("the report and the certificate disagree: %q and %q", scan.Serial, scan.Certificate.Serial)
	}
}

// Let's Encrypt's leaves no longer name an OCSP responder, so there is nothing
// to staple, and a notice about it appeared on every one of their sites.
func TestNoOCSPNoticeOnlyWhenTheLeafNamesAResponder(t *testing.T) {
	_, withoutOCSP := scanTestCert(t, 2, nil)
	scan := goodScan()
	scan.OCSPStapled = false
	describeChain(scan, []*x509.Certificate{withoutOCSP}, "scan.test")
	grade(scan)
	if hasFinding(scan, "tls.no-ocsp") {
		t.Fatalf("a leaf with no responder has nothing to staple: %+v", scan.Findings)
	}

	_, withOCSP := scanTestCert(t, 3, []string{"http://ocsp.example.test"})
	scan = goodScan()
	scan.OCSPStapled = false
	describeChain(scan, []*x509.Certificate{withOCSP}, "scan.test")
	grade(scan)
	if !hasFinding(scan, "tls.no-ocsp") {
		t.Fatalf("a responder and no staple should be noted: %+v", scan.Findings)
	}
}

// A TLS service that is not a website — a mail server on 993 — gave no HTTP
// response, and the report called that a missing HSTS header.
func TestAServiceThatIsNotAWebsiteIsNotGradedOnHeaders(t *testing.T) {
	cert, _ := scanTestCert(t, 4, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}}, func(c *tls.Conn) {
		c.Write([]byte("* OK IMAP4rev1 ready\r\n"))
		io.Copy(io.Discard, io.LimitReader(c, 4096))
	})
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
	}))
	defer plain.Close()

	result := scanHTTP(context.Background(), "https://"+addr+"/", plain.URL+"/", tlsOffer("", 0, 0))
	if result.Service != "other" || result.Banner != "* OK IMAP4rev1 ready" || result.HTTPSError != "" {
		t.Fatalf("the IMAP greeting should say the service is not a website: %+v", result)
	}
	if plainHits.Load() != 0 {
		t.Error("port 80 belongs to another service and should not be graded for this one")
	}

	scan := goodScan()
	scan.HTTP = result
	grade(scan)
	// A mail server has nothing to fix on the HTTP side, so nothing is said
	// there at all.
	for _, id := range []string{"tls.no-hsts", "tls.no-redirect", "http.header.", "http.https-error"} {
		if hasFinding(scan, id) {
			t.Errorf("%s reported for a service that is not a website: %+v", id, scan.Findings)
		}
	}
}

// A request that got no answer at all could be a failing website or a quiet
// service, so that is said, and still nothing is graded on headers.
func TestAnHTTPSRequestWithNoAnswerIsSaidAndNotGraded(t *testing.T) {
	cert, _ := scanTestCert(t, 11, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}}, func(*tls.Conn) {})
	result := scanHTTP(context.Background(), "https://"+addr+"/", "http://127.0.0.1:1/", tlsOffer("", 0, 0))
	if result.Service != "unknown" || result.Banner != "" || result.HTTPSError == "" {
		t.Fatalf("got %+v", result)
	}
	scan := goodScan()
	scan.HTTP = result
	grade(scan)
	if !hasFinding(scan, "http.https-error") || hasFinding(scan, "tls.no-hsts") {
		t.Fatalf("findings %+v", scan.Findings)
	}
}

// The whole scan of the same listener: reachable, and the HTTP half says it
// got no HTTP answer instead of inventing findings.
func TestScanTLSOfANonHTTPService(t *testing.T) {
	cert, _ := scanTestCert(t, 5, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		func(c *tls.Conn) { c.Write([]byte("* OK ready\r\n")) })
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)

	scan := ScanTLS(context.Background(), host, port)
	if !scan.Reachable || scan.HTTP == nil || scan.HTTP.Service != "other" || scan.HTTP.Banner != "* OK ready" {
		t.Fatalf("got %+v", scan)
	}
	if protocolStatus(scan.Protocols, "TLS 1.0") != "refused" {
		t.Errorf("protocols = %+v", scan.Protocols)
	}
	if hasFinding(scan, "tls.no-hsts") {
		t.Errorf("HSTS judged on no response: %+v", scan.Findings)
	}
}

// httpsWithHeaders is the HTTPS side the plain-HTTP tests share: an answer
// with HSTS, so only the redirect is under test.
func httpsWithHeaders(t *testing.T) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("Server", "nginx")
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

// redirects answers each path with a redirect to the mapped location, /drop
// by closing the connection, and anything unmapped with 200.
func redirects(t *testing.T, to func(base string) map[string]string) string {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/drop" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		if location, ok := to(base)[r.URL.Path]; ok {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusMovedPermanently)
		}
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv.URL
}

// Only the first hop used to be read, so http://x → http://www.x →
// https://www.x was graded "does not redirect to HTTPS".
func TestThePlainHTTPRedirectIsFollowedToHTTPS(t *testing.T) {
	cases := []struct {
		name      string
		to        func(base string) map[string]string
		redirects bool
		hops      int
		verdict   string
		detail    string
	}{
		{"straight to HTTPS on the same host", func(string) map[string]string {
			return map[string]string{"/": "https://127.0.0.1/"}
		}, true, 1, "same-host", ""},
		{"straight to HTTPS on another host", func(string) map[string]string {
			return map[string]string{"/": "https://example.test/"}
		}, true, 1, "other-host", ""},
		{"through another host first", func(base string) map[string]string {
			return map[string]string{"/": base + "/www", "/www": "https://www.example.test/"}
		}, true, 2, "other-host", ""},
		{"a relative hop first", func(string) map[string]string {
			return map[string]string{"/": "/login", "/login": "https://127.0.0.1/login"}
		}, true, 2, "same-host", ""},
		{"no redirect", func(string) map[string]string { return map[string]string{} },
			false, 1, "stays-http", "answered 200"},
		{"to a page that stays on HTTP", func(base string) map[string]string {
			return map[string]string{"/": base + "/home"}
		}, false, 2, "stays-http", "which answered 200"},
		{"a loop", func(string) map[string]string {
			return map[string]string{"/": "/a", "/a": "/"}
		}, false, 2, "loop", "loop back to"},
		{"more hops than it follows", func(string) map[string]string {
			return map[string]string{"/": "/1", "/1": "/2", "/2": "/3", "/3": "/4", "/4": "/5", "/5": "/6"}
		}, false, maxRedirectHops, "too-many", "without reaching HTTPS"},
		{"a hop that does not answer", func(string) map[string]string {
			return map[string]string{"/": "/drop"}
		}, false, 2, "dead-end", "did not answer"},
		{"a redirect to another scheme", func(string) map[string]string {
			return map[string]string{"/": "ftp://example.test/"}
		}, false, 1, "dead-end", "without reaching HTTPS"},
	}
	httpsURL := httpsWithHeaders(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plain := redirects(t, c.to)
			result := scanHTTP(context.Background(), httpsURL, plain+"/", tlsOffer("", 0, 0))
			if result.PlainRedirects != c.redirects || len(result.RedirectChain) != c.hops || result.RedirectVerdict != c.verdict {
				t.Fatalf("redirects=%v (%s) with %d hops, want %v (%s) with %d: %+v",
					result.PlainRedirects, result.RedirectVerdict, len(result.RedirectChain), c.redirects, c.verdict, c.hops, result.RedirectChain)
			}
			if result.RedirectChain[0].URL != plain+"/" || result.PlainStatus != result.RedirectChain[0].Status {
				t.Errorf("the first hop is not the plain request: %+v", result.RedirectChain[0])
			}
			scan := goodScan()
			scan.HTTP = result
			grade(scan)
			if got := hasFinding(scan, "tls.no-redirect"); got == c.redirects {
				t.Fatalf("no-redirect finding = %v: %+v", got, scan.Findings)
			}
			if !c.redirects {
				for _, f := range scan.Findings {
					if f.ID == "tls.no-redirect" && !strings.Contains(f.Detail, c.detail) {
						t.Errorf("detail %q does not say %q", f.Detail, c.detail)
					}
				}
			}
		})
	}
}

// A closed port 80 is not a missing redirect, and the page may only call it
// refused when it was.
func TestAClosedPort80IsNamedForWhatHappened(t *testing.T) {
	result := scanHTTP(context.Background(), httpsWithHeaders(t), "http://127.0.0.1:1/", tlsOffer("", 0, 0))
	if result.PlainError == "" || result.PlainErrorKind != "refused" {
		t.Fatalf("got %q (%s)", result.PlainError, result.PlainErrorKind)
	}
	if len(result.RedirectChain) != 0 {
		t.Errorf("nothing answered, so there is no chain: %+v", result.RedirectChain)
	}
	if strings.HasPrefix(result.PlainError, "Get ") {
		t.Errorf("the URL is already on the page: %q", result.PlainError)
	}
	scan := goodScan()
	scan.HTTP = result
	grade(scan)
	if hasFinding(scan, "tls.no-redirect") {
		t.Error("a closed port 80 is not a missing redirect")
	}
}

func TestRequestErrorSaysWhatHappened(t *testing.T) {
	closed := &url.Error{Op: "Get", URL: "https://mail.example.test/", Err: io.EOF}
	if got := requestError(closed); got != "the server closed the connection without an HTTP response" {
		t.Errorf("EOF read as %q", got)
	}
	refused := &url.Error{Op: "Get", URL: "http://127.0.0.1:1/", Err: errString("dial tcp 127.0.0.1:1: connect: connection refused")}
	if got := requestError(refused); got != "dial tcp 127.0.0.1:1: connect: connection refused" {
		t.Errorf("got %q", got)
	}
}

func TestNetErrorKind(t *testing.T) {
	for _, c := range []struct {
		err  error
		kind string
	}{
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "refused"},
		{&net.DNSError{Err: "no such host", Name: "nowhere.test", IsNotFound: true}, "dns"},
		{context.DeadlineExceeded, "timeout"},
		{errString("something"), "other"},
	} {
		if got := netErrorKind(c.err); got != c.kind {
			t.Errorf("%v = %s, want %s", c.err, got, c.kind)
		}
	}
}

// Go leaves RSA key exchange out of what it offers by default, so a server
// taking TLS 1.0 only with AES128-SHA answered the probe with a handshake
// failure and was reported as refusing it: grade A for a server that should
// have had C. The probes now offer every suite Go implements.
func TestProbeProtocolsOffersEverySuiteGoHas(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS10,
		MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, // TLS 1.2 only
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,          // what 1.0 and 1.1 get
		},
	}
	srv.StartTLS()
	defer srv.Close()

	scan := goodScan()
	scan.Protocols = probeProtocols(context.Background(), srv.Listener.Addr().String(), "example.com", "")
	for name, want := range map[string]string{
		"TLS 1.0": "offered", "TLS 1.1": "offered", "TLS 1.2": "offered", "TLS 1.3": "refused",
	} {
		if got := protocolStatus(scan.Protocols, name); got != want {
			t.Errorf("%s = %s, want %s: %+v", name, got, want, scan.Protocols)
		}
	}
	grade(scan)
	if scan.Grade != "C" || !hasFinding(scan, "tls.old-protocol.TLS 1.0") {
		t.Fatalf("grade %s with %+v", scan.Grade, scan.Findings)
	}
}

// Every hop after the first goes where the remote site says. A site that
// redirected to a service on loopback had this server GET its admin path,
// and that service's own redirect came back in the report.
func TestARedirectIsNotFollowedIntoThisMachine(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHits.Add(1)
		http.Redirect(w, r, "http://secret.internal.test/token=abc", http.StatusFound)
	}))
	defer internal.Close()
	_, internalPort, _ := net.SplitHostPort(internal.Listener.Addr().String())
	httpsURL := httpsWithHeaders(t)

	for _, target := range []string{
		internal.URL + "/admin/delete?x=1",
		// A name is judged by the address it resolves to.
		"http://localhost:" + internalPort + "/admin/delete?x=1",
	} {
		plain := redirects(t, func(string) map[string]string { return map[string]string{"/": target} })
		result := scanHTTP(context.Background(), httpsURL, plain+"/", tlsOffer("", 0, 0))
		if internalHits.Load() != 0 {
			t.Fatalf("%s was requested", target)
		}
		chain := result.RedirectChain
		if len(chain) != 2 || chain[0].Location != target || chain[1].URL != target ||
			!chain[1].Internal || chain[1].Status != 0 || chain[1].Error != "" || result.PlainRedirects ||
			result.RedirectVerdict != "internal" {
			t.Fatalf("chain = %+v (%s)", chain, result.RedirectVerdict)
		}
		scan := goodScan()
		scan.HTTP = result
		grade(scan)
		if hasFinding(scan, "tls.no-redirect") || !hasFinding(scan, "http.redirect-internal") || scan.Grade != "A" {
			t.Fatalf("grade %s with %+v", scan.Grade, scan.Findings)
		}
		for _, f := range scan.Findings {
			if f.ID == "http.redirect-internal" && !strings.Contains(f.Detail, "private network") {
				t.Errorf("detail %q", f.Detail)
			}
		}
	}
}

func TestHopAllowed(t *testing.T) {
	for _, c := range []struct {
		address string
		allowed bool
	}{
		{"127.0.0.1:80", true}, // where the first request went
		{"127.0.0.1:8080", false},
		{"[::1]:80", false},
		{"10.0.0.5:80", false},
		{"192.168.1.10:80", false},
		{"169.254.169.254:80", false},
		{"100.100.100.100:80", false},
		{"0.0.0.0:80", false},
		{"[fd00::1]:80", false},
		{"[fe80::1%eth0]:80", false},
		{"93.184.215.14:80", true},
		{"93.184.215.14:8080", true},
		{"[2606:4700::1]:80", true},
	} {
		if got := hopAllowed("127.0.0.1:80", c.address); got != c.allowed {
			t.Errorf("%s = %v, want %v", c.address, got, c.allowed)
		}
	}
}

// A server taking TLS 1.2 only with RSA key exchange answers a current
// client's offer with an alert, and the report said nothing answered and
// advised checking DNS. Offered everything this library has, it connects, so
// it is reachable with the one finding that matters, and the HTTP half is
// asked with the offer that worked: with the current one it failed the same
// handshake and said the service was not a website.
func TestScanTLSReachesAServerThatTakesOnlyRSAKeyExchange(t *testing.T) {
	addr := tlsListener(t, &tls.Config{
		Certificates: []tls.Certificate{rsaTestCert(t)},
		MinVersion:   tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256},
	}, func(c *tls.Conn) { c.Write([]byte("* OK ready\r\n")) })
	host, port := splitAddr(t, addr)

	scan := ScanTLS(context.Background(), host, port)
	if !scan.Reachable || !scan.LegacyOnly || scan.Error != "" {
		t.Fatalf("reachable=%v legacy=%v error=%q", scan.Reachable, scan.LegacyOnly, scan.Error)
	}
	if scan.Negotiated != "TLS 1.2" || scan.CipherSuite != "TLS_RSA_WITH_AES_128_GCM_SHA256" || scan.Certificate == nil {
		t.Fatalf("negotiated %s with %s, certificate %v", scan.Negotiated, scan.CipherSuite, scan.Certificate)
	}
	if scan.Grade != "F" || !hasFinding(scan, "tls.legacy-only") || hasFinding(scan, "tls.unreachable") {
		t.Fatalf("grade %s with %+v", scan.Grade, scan.Findings)
	}
	for _, f := range scan.Findings {
		if f.ID == "tls.legacy-only" && !strings.Contains(f.Detail, "took TLS 1.2 with TLS_RSA_WITH_AES_128_GCM_SHA256") {
			t.Errorf("the finding should say what was taken: %q", f.Detail)
		}
	}
	if strings.Contains(scan.Summary, "Nothing answered") || !strings.Contains(scan.Summary, "current client") {
		t.Errorf("summary %q", scan.Summary)
	}
	if scan.HTTP == nil || scan.HTTP.Service != "other" || scan.HTTP.Banner != "* OK ready" {
		t.Errorf("the HTTPS request should have reached the service and heard its greeting: %+v", scan.HTTP)
	}
}

// An alert is the server's answer, so the report may not say nothing answered
// or send the reader to check DNS; a closed port still reads as silence.
func TestScanTLSSaysARefusedHandshakeWasRefused(t *testing.T) {
	addr := tlsListener(t, &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, errors.New("no certificate for this name")
		},
	}, func(*tls.Conn) {})
	host, port := splitAddr(t, addr)

	scan := ScanTLS(context.Background(), host, port)
	if scan.Reachable || scan.LegacyOnly || scan.Grade != "F" {
		t.Fatalf("got %+v", scan)
	}
	if !hasFinding(scan, "tls.refused") || hasFinding(scan, "tls.unreachable") {
		t.Fatalf("findings %+v", scan.Findings)
	}
	f := scan.Findings[0]
	if !strings.Contains(f.Detail, "It answered internal error") || strings.Contains(f.Advice, "resolves") {
		t.Errorf("finding %+v", f)
	}
	if strings.Contains(scan.Summary, "Nothing answered") || !strings.Contains(scan.Summary, "refused the handshake") {
		t.Errorf("summary %q", scan.Summary)
	}

	if scan.Failure == nil || scan.Failure.Stage != "handshake" || scan.Failure.Reason != "alert" || scan.Failure.Alert != "internal error" {
		t.Errorf("failure %+v", scan.Failure)
	}

	closed := ScanTLS(context.Background(), "127.0.0.1", 1)
	if !hasFinding(closed, "tcp.refused") || closed.Summary != "127.0.0.1:1 refused the connection." {
		t.Fatalf("a closed port: %q %+v", closed.Summary, closed.Findings)
	}
}

// One alert is not a policy: when the current offer is taken on the second
// try, the server is not reported as taking only older ones.
func TestAPassingAlertIsNotReportedAsLegacyOnly(t *testing.T) {
	cert, _ := scanTestCert(t, 9, nil)
	var calls atomic.Int32
	addr := tlsListener(t, &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("not ready yet")
			}
			return &cert, nil
		},
	}, func(*tls.Conn) {})

	conn, legacy, err := handshake(context.Background(), addr, "scan.test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if legacy || conn.ConnectionState().Version != tls.VersionTLS13 || calls.Load() != 3 {
		t.Fatalf("legacy=%v version=%x after %d handshakes", legacy, conn.ConnectionState().Version, calls.Load())
	}
}

// The HTTPS request of a legacy-only server offers what it takes; the one a
// current client makes fails the handshake.
func TestScanHTTPOffersWhatItIsGiven(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}))
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256},
	}
	srv.StartTLS()
	defer srv.Close()

	current := scanHTTP(context.Background(), srv.URL+"/", "http://127.0.0.1:1/", tlsOffer("", 0, 0))
	if !strings.Contains(current.HTTPSError, "handshake failure") {
		t.Fatalf("the current offer should be refused: %+v", current)
	}
	everything := scanHTTP(context.Background(), srv.URL+"/", "http://127.0.0.1:1/", tlsOffer("", oldestVersion, newestVersion))
	if everything.HTTPSError != "" || everything.StatusCode != http.StatusOK || everything.HSTS == nil {
		t.Fatalf("got %+v", everything)
	}
}
