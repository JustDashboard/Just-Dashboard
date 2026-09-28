package proxysvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const day = 24 * time.Hour

// termCert is a leaf issued at notBefore for term, as the Certificates page
// and the TLS report both read it.
func termCert(t *testing.T, notBefore time.Time, term time.Duration) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "scan.test"},
		DNSNames:  []string{"scan.test"},
		NotBefore: notBefore,
		// notAfter is the last valid second, as a CA writes it.
		NotAfter:              notBefore.Add(term - time.Second),
		CRLDistributionPoints: []string{"http://crl.example.test/1.crl"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func TestRenewalWindowIsAShareOfTheTerm(t *testing.T) {
	issued := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name   string
		term   time.Duration
		window time.Duration
	}{
		{"Let's Encrypt's ninety days", 90 * day, 30 * day},
		{"forty-five days", 45 * day, 15 * day},
		{"Let's Encrypt's six-day profile, half of it", 160 * time.Hour, 80 * time.Hour},
		{"ten days is still short-lived", 10 * day, 5 * day},
		{"a year is capped at thirty days", 398 * day, 30 * day},
	} {
		if got := renewalWindow(issued, issued.Add(c.term)); got != c.window {
			t.Errorf("%s: window %s, want %s", c.name, got, c.window)
		}
	}
	if got := renewalWindow(time.Time{}, issued); got != maxRenewalWindow {
		t.Errorf("a certificate with no start should get the old thirty days, got %s", got)
	}
}

// The Certificates page's "expiring" and the TLS report are the same rule, so
// they never disagree about one certificate: a six-day certificate is not
// expiring for its whole life, and a ninety-day one is from thirty days out.
func TestSummariseJudgesExpiryAgainstTheTerm(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name     string
		term     time.Duration
		left     time.Duration
		expiring bool
	}{
		{"six-day, five days left", 160 * time.Hour, 5 * day, false},
		{"six-day, three days left", 160 * time.Hour, 3 * day, true},
		{"ninety-day, forty days left", 90 * day, 40 * day, false},
		{"ninety-day, twenty days left", 90 * day, 20 * day, true},
		{"a year, sixty days left", 365 * day, 60 * day, false},
		{"a year, twenty days left", 365 * day, 20 * day, true},
	} {
		leaf := termCert(t, now.Add(c.left-c.term), c.term)
		if got := summarise(leaf, "scan.test", "").Expiring; got != c.expiring {
			t.Errorf("%s: expiring = %v", c.name, got)
		}
	}
	expired := termCert(t, now.Add(-10*day), 5*day)
	if cert := summarise(expired, "scan.test", ""); cert.Expiring || !cert.Expired {
		t.Errorf("an expired certificate is expired, not expiring: %+v", cert)
	}
}

// graded scans a certificate of term with left to run.
func graded(t *testing.T, term, left time.Duration) *TLSScan {
	t.Helper()
	scan := goodScan()
	leaf := termCert(t, time.Now().Add(left-term), term)
	scan.Certificate = summarise(leaf, "example.com", "")
	grade(scan)
	return scan
}

func TestExpiryIsGradedAgainstTheTerm(t *testing.T) {
	for _, c := range []struct {
		name    string
		term    time.Duration
		left    time.Duration
		grade   string
		finding string
	}{
		// The old rule graded every short-lived certificate B and said its
		// renewal was not running.
		{"six-day, five days left", 160 * time.Hour, 5 * day, "A+", ""},
		{"six-day, due", 160 * time.Hour, 60 * time.Hour, "A+", "tls.renewal-due"},
		{"six-day, overdue", 160 * time.Hour, 30 * time.Hour, "B", "tls.expiring"},
		{"ninety-day, forty days left", 90 * day, 40 * day, "A+", ""},
		// Between 15 and 30 days the Certificates page said expiring and the
		// TLS report said nothing.
		{"ninety-day, due", 90 * day, 20 * day, "A+", "tls.renewal-due"},
		{"ninety-day, overdue", 90 * day, 10 * day, "B", "tls.expiring"},
	} {
		scan := graded(t, c.term, c.left)
		if scan.Grade != c.grade {
			t.Errorf("%s: grade %s with %+v", c.name, scan.Grade, scan.Findings)
		}
		for _, id := range []string{"tls.renewal-due", "tls.expiring"} {
			if hasFinding(scan, id) != (id == c.finding) {
				t.Errorf("%s: %s = %v: %+v", c.name, id, hasFinding(scan, id), scan.Findings)
			}
		}
	}
	scan := graded(t, 160*time.Hour, 30*time.Hour)
	for _, f := range scan.Findings {
		if f.ID == "tls.expiring" {
			if f.Title != "The certificate expires in 29 hours" || !strings.Contains(f.Detail, "the last 80 of its 160 hours") {
				t.Errorf("finding %+v", f)
			}
			if strings.Contains(f.Detail+f.Advice, "30 days") {
				t.Errorf("a six-day certificate was judged by the thirty-day rule: %+v", f)
			}
		}
	}
}

func TestDescribeChainRecordsTheTermAndThePins(t *testing.T) {
	leaf := termCert(t, time.Now().Add(-10*day), 90*day)
	scan := &TLSScan{Chain: []ChainLink{}}
	describeChain(scan, []*x509.Certificate{leaf}, "scan.test")
	if scan.LifetimeHours != 90*24 || scan.RenewalWindowHours != 30*24 {
		t.Errorf("term %d hours, window %d hours", scan.LifetimeHours, scan.RenewalWindowHours)
	}
	if len(scan.CRLURLs) != 1 || scan.CRLURLs[0] != "http://crl.example.test/1.crl" {
		t.Errorf("CRL %v", scan.CRLURLs)
	}
	spki, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(spki)
	if want := base64.StdEncoding.EncodeToString(sum[:]); scan.SPKIPin != want {
		t.Errorf("pin %s, want %s", scan.SPKIPin, want)
	}
}

// The listeners below answer a TLS handshake the ways that were all reported
// as "Nothing answered".

func listen(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
			go serve(conn)
		}
	}()
	return ln.Addr().String()
}

func TestClassifyDialError(t *testing.T) {
	alerting := tlsListener(t, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return nil, errors.New("no certificate")
	}}, func(*tls.Conn) {})
	plainHTTP := httptest.NewServer(http.NotFoundHandler())
	defer plainHTTP.Close()
	ssh := listen(t, func(c net.Conn) {
		c.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
		io.Copy(io.Discard, io.LimitReader(c, 4096))
		c.Close()
	})
	closing := listen(t, func(c net.Conn) { c.Close() })
	silent := listen(t, func(c net.Conn) { time.Sleep(2 * time.Second); c.Close() })

	dial := func(addr string, timeout time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		conn, err := dialTLS(ctx, addr, "scan.test", 0, 0)
		if err == nil {
			conn.Close()
		}
		return err
	}
	timeout := &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 443},
		Err: os.NewSyscallError("connect", syscall.ETIMEDOUT)}
	for _, c := range []struct {
		name            string
		err             error
		stage, reason   string
		address, answer string
	}{
		{"no record", &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "nowhere.test", IsNotFound: true}},
			"dns", "no-such-host", "", ""},
		{"a resolver that timed out", &net.OpError{Op: "dial", Err: &net.DNSError{Err: "i/o timeout", Name: "slow.test", IsTimeout: true}},
			"dns", "timeout", "", ""},
		{"a closed port", dial("127.0.0.1:1", 5*time.Second), "connect", "refused", "127.0.0.1:1", ""},
		{"a dropped connection", timeout, "connect", "timeout", "203.0.113.9:443", ""},
		{"no route", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)},
			"connect", "unreachable", "", ""},
		{"another dial failure", &net.OpError{Op: "dial", Err: os.NewSyscallError("socket", syscall.EMFILE)},
			"connect", "error", "", ""},
		{"a server's alert", dial(alerting, 5*time.Second), "handshake", "alert", alerting, ""},
		// nginx's `listen 443;` without ssl answers the ClientHello with 400.
		{"plain HTTP on the port", dial(plainHTTP.Listener.Addr().String(), 5*time.Second),
			"handshake", "plain-http", plainHTTP.Listener.Addr().String(), "HTTP/"},
		{"another protocol on the port", dial(ssh, 5*time.Second), "handshake", "not-tls", ssh, "SSH-2"},
		{"a close on the hello", dial(closing, 5*time.Second), "handshake", "closed", closing, ""},
		{"no answer to the hello", dial(silent, 300*time.Millisecond), "handshake", "timeout", silent, ""},
	} {
		got := classifyDialError(c.err)
		if got.Stage != c.stage || got.Reason != c.reason || got.Address != c.address || got.Answer != c.answer {
			t.Errorf("%s (%v): got %+v, want %s/%s at %q answering %q", c.name, c.err, got, c.stage, c.reason, c.address, c.answer)
		}
	}
	if got := classifyDialError(dial(alerting, 5*time.Second)); got.Alert != "internal error" {
		t.Errorf("the alert should be the server's words: %+v", got)
	}
	// A connection that failed is not called a failed handshake.
	scan := &TLSScan{Domain: "example.com", Port: 443}
	failure := ScanFailure{Stage: "connect", Reason: "error"}
	if f := failureFinding(scan, failure, errors.New("dial tcp: socket: too many open files")); f.ID != "tcp.failed" || strings.Contains(f.Title, "handshake") {
		t.Errorf("finding %+v", f)
	}
}

func TestWhereConnected(t *testing.T) {
	local := []string{"127.0.0.1", "10.0.0.4", "203.0.113.4"}
	public := []string{"203.0.113.4"}
	for _, c := range []struct {
		name    string
		address string
		dns     *DomainCheck
		public  []string
		where   string
	}{
		{"loopback", "127.0.0.1:443", nil, public, "here"},
		{"a private address on this server", "10.0.0.4:443", nil, public, "here"},
		{"this server's public address", "203.0.113.4:443", nil, public, "here"},
		{"Cloudflare", "104.16.1.1:443", nil, public, "cloudflare"},
		{"another host", "198.51.100.7:443", nil, public, "elsewhere"},
		// Behind provider NAT this server's public address is on no
		// interface, so another address may still be this server.
		{"another address behind NAT", "198.51.100.7:443", nil, nil, "unknown"},
		{"no address, the name points here", "", &DomainCheck{Addresses: []string{"203.0.113.4"}, PointsHere: true}, public, "here"},
		{"no address, the name points elsewhere", "", &DomainCheck{Addresses: []string{"198.51.100.7"}}, public, "elsewhere"},
		{"no address and no name", "", nil, public, "unknown"},
	} {
		if got := whereConnected(c.address, c.dns, local, c.public); got != c.where {
			t.Errorf("%s: %s, want %s", c.name, got, c.where)
		}
	}

	// A dual-stack VM: its IPv6 is on the interface, and its public IPv4
	// (203.0.113.10) is mapped in front of it by the provider, so an IPv4
	// address it does not see may still be its own.
	local = []string{"127.0.0.1", "10.0.0.4", "2001:db8::13"}
	public = []string{"2001:db8::13"}
	for _, c := range []struct {
		name    string
		address string
		dns     *DomainCheck
		where   string
	}{
		{"its own IPv6", "[2001:db8::13]:443", nil, "here"},
		{"another IPv6 address", "[2001:db8::99]:443", nil, "elsewhere"},
		{"an IPv4 address, perhaps its mapped one", "203.0.113.10:443", nil, "unknown"},
		{"no address, the name has only an A record", "", &DomainCheck{Addresses: []string{"203.0.113.10"}}, "unknown"},
		{"no address, the name has only another AAAA", "", &DomainCheck{Addresses: []string{"2001:db8::99"}}, "elsewhere"},
		{"no address, the name has both", "", &DomainCheck{Addresses: []string{"203.0.113.10", "2001:db8::99"}}, "unknown"},
	} {
		if got := whereConnected(c.address, c.dns, local, public); got != c.where {
			t.Errorf("dual stack, %s: %s, want %s", c.name, got, c.where)
		}
	}
}

// A failed scan's advice is for whoever answered: nginx advice for this
// server or a named other host, Cloudflare's own ports for its proxy, and no
// record to point for an address scanned as itself.
func TestFailureAdviceFollowsWhereTheAnswerCameFrom(t *testing.T) {
	dns := &DomainCheck{HostAddresses: []string{"198.51.100.4"}, HostAddressesKnown: true}
	advise := func(domain string, port int, stage, reason, address, where string) string {
		scan := &TLSScan{Domain: domain, Port: port}
		f := ScanFailure{Stage: stage, Reason: reason, Address: address, Where: where, DNS: dns, Answer: "HTTP/1.1 400"}
		return failureFinding(scan, f, errors.New("the error")).Advice
	}
	for _, c := range []struct {
		name          string
		advice        string
		want, wantNot []string
	}{
		{"plain HTTP on Cloudflare's plain-HTTP port",
			advise("cloudflare.com", 8080, "handshake", "plain-http", "[2606:4700::6810:84e5]:8080", "cloudflare"),
			[]string{"[2606:4700::6810:84e5]:8080 is Cloudflare's proxy, not this server.", "Port 8080 is one of the ports its edge serves as plain HTTP", "443, 2053, 2083, 2087, 2096 and 8443"},
			[]string{"nginx", "listen"}},
		{"plain HTTP from Cloudflare on another port",
			advise("example.com", 9000, "handshake", "plain-http", "104.16.1.1:9000", "cloudflare"),
			[]string{"Cloudflare's proxy, not this server.", cloudflareHandshake},
			[]string{"nginx", "listen", "is one of the ports"}},
		{"plain HTTP from another host",
			advise("example.com", 8443, "handshake", "plain-http", "203.0.113.7:8443", "elsewhere"),
			[]string{"203.0.113.7:8443 is not this server.", "listen 8443 ssl;", "on that host", "point its record at 198.51.100.4"},
			[]string{"Cloudflare"}},
		{"plain HTTP here",
			advise("example.com", 8443, "handshake", "plain-http", "127.0.0.1:8443", "here"),
			[]string{"That is this server. In nginx a listen directive without ssl", "listen 8443 ssl;"},
			[]string{"that host", "Cloudflare"}},
		// Plain HTTP is port 80's own protocol: `listen 80 ssl;` there would
		// break http://, the redirect to HTTPS and HTTP-01 renewal.
		{"plain HTTP on port 80 here",
			advise("127.0.0.1", 80, "handshake", "plain-http", "127.0.0.1:80", "here"),
			[]string{"That is this server. Port 80 is HTTP's own port, so a plain HTTP answer there is right", "scan 127.0.0.1 on port 443."},
			[]string{"listen", "ssl;", "nginx", "that host"}},
		{"plain HTTP on port 80 elsewhere",
			advise("github.com", 80, "handshake", "plain-http", "140.82.121.3:80", "elsewhere"),
			[]string{"140.82.121.3:80 is not this server. Port 80 is HTTP's own port", "scan github.com on port 443.", "point its record at 198.51.100.4"},
			[]string{"listen", "ssl;", "nginx", "Cloudflare"}},
		{"plain HTTP on port 80 that may be here, said once",
			advise("example.com", 80, "handshake", "plain-http", "203.0.113.10:80", "unknown"),
			[]string{"public IPv4 address in front of it. Port 80 is HTTP's own port", "scan example.com on port 443."},
			[]string{"listen", "ssl;", "If it is:", "If not:"}},
		{"plain HTTP on port 80 from Cloudflare",
			advise("example.com", 80, "handshake", "plain-http", "104.16.1.1:80", "cloudflare"),
			[]string{"Cloudflare's proxy, not this server.", "Port 80 is one of the ports its edge serves as plain HTTP"},
			[]string{"listen", "ssl;"}},
		{"another protocol on Cloudflare",
			advise("example.com", 443, "handshake", "not-tls", "104.16.1.1:443", "cloudflare"),
			[]string{"Cloudflare's proxy, not this server.", cloudflareHandshake},
			[]string{"STARTTLS"}},
		{"another protocol on another host",
			advise("example.com", 443, "handshake", "not-tls", "203.0.113.7:443", "elsewhere"),
			[]string{"203.0.113.7:443 is not this server. Another service owns this port"},
			nil},
		{"a close from Cloudflare",
			advise("example.com", 443, "handshake", "closed", "104.16.1.1:443", "cloudflare"),
			[]string{cloudflareHandshake}, []string{"a proxy passing the connection"}},
		{"silence from Cloudflare",
			advise("example.com", 443, "handshake", "timeout", "104.16.1.1:443", "cloudflare"),
			[]string{cloudflareHandshake}, []string{"database"}},
		{"Cloudflare refusing a name",
			advise("a.b.example.com", 443, "handshake", "alert", "104.16.1.1:443", "cloudflare"),
			[]string{"Cloudflare's proxy, not this server.", "Universal certificate"},
			[]string{"ssl_reject_handshake"}},
		{"Cloudflare refusing an address",
			advise("104.16.1.1", 443, "handshake", "alert", "104.16.1.1:443", "cloudflare"),
			[]string{"scan the name instead"}, []string{"ssl_reject_handshake", "Universal"}},
		{"a refusal that may be here, said once",
			advise("example.com", 443, "handshake", "alert", "203.0.113.10:443", "unknown"),
			[]string{"Whether 203.0.113.10:443 is this server cannot be told from here, because the provider maps this server's public IPv4 address in front of it. Something on port 443"},
			[]string{"If it is:", "If not:"}},
		{"a timeout that may be here, both ways",
			advise("example.com", 443, "connect", "timeout", "203.0.113.10:443", "unknown"),
			[]string{"public IPv4 address", "If it is: A firewall is most likely dropping it", "If not: A firewall there"},
			nil},
		{"a timeout on another host's address scanned as itself",
			advise("192.0.2.1", 443, "connect", "timeout", "192.0.2.1:443", "elsewhere"),
			[]string{"192.0.2.1:443 is not this server. A firewall there is dropping it"},
			[]string{"record"}},
		{"a timeout on another host a name points at",
			advise("example.com", 443, "connect", "timeout", "192.0.2.1:443", "elsewhere"),
			[]string{"If it should be served here, point its record at 198.51.100.4."}, nil},
		{"a refusal with no address, the name resolving to Cloudflare",
			advise("example.com", 22, "connect", "refused", "", "cloudflare"),
			[]string{"example.com resolves to Cloudflare's proxy, not this server. " + cloudflareOtherPorts}, nil},
		{"a refusal with no address, the name resolving elsewhere",
			advise("example.com", 443, "connect", "refused", "", "elsewhere"),
			[]string{"example.com does not resolve to this server. The refusal is that host's."}, nil},
	} {
		for _, want := range c.want {
			if !strings.Contains(c.advice, want) {
				t.Errorf("%s: %q lacks %q", c.name, c.advice, want)
			}
		}
		for _, not := range c.wantNot {
			if strings.Contains(c.advice, not) {
				t.Errorf("%s: %q says %q", c.name, c.advice, not)
			}
		}
	}
}

// A failed scan names its stage, is graded F and carries advice for that
// stage, not "check the domain resolves" for every one of them.
func TestScanTLSDiagnosesWhereItStopped(t *testing.T) {
	closed := ScanTLS(context.Background(), "127.0.0.1", 1)
	f := closed.Failure
	if closed.Grade != "F" || f == nil || f.Stage != "connect" || f.Reason != "refused" || f.Where != "here" || f.DNS == nil {
		t.Fatalf("closed port: %+v %+v", closed, f)
	}
	if !hasFinding(closed, "tcp.refused") || !strings.Contains(closed.Findings[0].Advice, "That is this server.") ||
		!strings.Contains(closed.Findings[0].Title, "Nothing is listening on port 1") {
		t.Errorf("finding %+v", closed.Findings)
	}

	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	host, port := splitAddr(t, plain.Listener.Addr().String())
	scan := ScanTLS(context.Background(), host, port)
	if scan.Failure == nil || scan.Failure.Reason != "plain-http" || scan.Summary != plain.Listener.Addr().String()+" answers plain HTTP, not TLS." {
		t.Fatalf("plain HTTP: %q %+v", scan.Summary, scan.Failure)
	}
	if !hasFinding(scan, "tls.plain-http") || !strings.Contains(scan.Findings[0].Advice, "listen "+plain.Listener.Addr().String()[len("127.0.0.1:"):]+" ssl;") {
		t.Errorf("finding %+v", scan.Findings)
	}
}

// net/http quotes a fragment of a first line that is not HTTP ("OK" of "* OK
// IMAP4rev1 ready"), so the greeting is read off the connection itself.
func TestTheGreetingOfAServiceThatIsNotHTTPIsKept(t *testing.T) {
	cert, _ := scanTestCert(t, 16, nil)
	for greeting, banner := range map[string]string{
		"* OK [CAPABILITY IMAP4rev1] Dovecot ready.\r\n":      "* OK [CAPABILITY IMAP4rev1] Dovecot ready.",
		"220 mail.example.test ESMTP Postfix\r\n250 more\r\n": "220 mail.example.test ESMTP Postfix",
		"+OK\x01POP3 ready\r\n":                               "+OK.POP3 ready",
		"*" + strings.Repeat("x", 300) + "\r\n":               "*" + strings.Repeat("x", 119) + "…",
	} {
		addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}}, func(c *tls.Conn) {
			c.Write([]byte(greeting))
			io.Copy(io.Discard, io.LimitReader(c, 4096))
		})
		result := scanHTTP(context.Background(), "https://"+addr+"/", "http://127.0.0.1:1/", tlsOffer("", 0, 0))
		if result.Service != "other" || result.Banner != banner {
			t.Errorf("%q: service %s, banner %q, error %q", greeting, result.Service, result.Banner, result.HTTPSError)
		}
	}
}

// A port registered to a service that is not a website is not asked for a
// web page, and has nothing graded on HTTP.
func TestARegisteredNonHTTPPortIsNotAskedForAWebPage(t *testing.T) {
	for port, name := range map[int]string{993: "IMAP", 995: "POP3", 465: "SMTP submission", 636: "LDAP"} {
		if implicitTLSServices[port] != name {
			t.Errorf("port %d = %q, want %q", port, implicitTLSServices[port], name)
		}
	}
	for _, port := range []int{443, 8443, 80} {
		if _, registered := implicitTLSServices[port]; registered {
			t.Errorf("port %d is a web port", port)
		}
	}
	scan := goodScan()
	scan.HTTP = &HTTPScan{Service: "other", ServiceName: "IMAP", Headers: []HeaderCheck{}, RedirectChain: []RedirectHop{}}
	grade(scan)
	if scan.Grade != "A" || len(scan.Findings) != 0 {
		t.Fatalf("grade %s with %+v", scan.Grade, scan.Findings)
	}
}

// preloadable is a scan of example.com that meets every rule of the list.
func preloadable() *TLSScan {
	scan := goodScan()
	scan.HTTP.HSTS = &HSTS{MaxAge: preloadMaxAge, IncludeSubDomains: true, Preload: true}
	scan.HTTP.RedirectChain = []RedirectHop{{URL: "http://example.com/", Status: 301, Location: "https://example.com/"}}
	return scan
}

var wwwAbsent = &PreloadRule{ID: "www", Title: "www.example.com serves HTTPS, if it exists", Passed: true}

func preloadRule(check *PreloadCheck, id string) *PreloadRule {
	for i := range check.Rules {
		if check.Rules[i].ID == id {
			return &check.Rules[i]
		}
	}
	return nil
}

func TestPreloadCheckFollowsTheListsRules(t *testing.T) {
	check := preloadCheck("example.com", preloadable(), wwwAbsent)
	if !check.Eligible || check.Domain != "example.com" {
		t.Fatalf("a domain meeting every rule: %+v", check)
	}
	ids := []string{}
	for _, rule := range check.Rules {
		ids = append(ids, rule.ID)
	}
	if strings.Join(ids, " ") != "registrable certificate redirect max-age include-subdomains preload www" {
		t.Errorf("rules %v", ids)
	}

	for _, c := range []struct {
		name   string
		change func(*TLSScan)
		www    *PreloadRule
		rule   string
		detail string
	}{
		// Six months was what the report said the list expects.
		{"six months", func(s *TLSScan) { s.HTTP.HSTS.MaxAge = hstsStrongMaxAge }, wwwAbsent, "max-age", "15552000 seconds (180 days); the list asks for a year"},
		{"no includeSubDomains", func(s *TLSScan) { s.HTTP.HSTS.IncludeSubDomains = false }, wwwAbsent, "include-subdomains", "leaves it out"},
		{"no preload", func(s *TLSScan) { s.HTTP.HSTS.Preload = false }, wwwAbsent, "preload", "does not ask"},
		{"no header", func(s *TLSScan) { s.HTTP.HSTS = nil }, wwwAbsent, "max-age", "No Strict-Transport-Security header"},
		{"www first", func(s *TLSScan) {
			s.HTTP.RedirectChain = []RedirectHop{
				{URL: "http://example.com/", Status: 301, Location: "http://www.example.com/"},
				{URL: "http://www.example.com/", Status: 301, Location: "https://www.example.com/"},
			}
		}, wwwAbsent, "redirect", "redirects to http://www.example.com/ first"},
		{"another host over HTTPS", func(s *TLSScan) {
			s.HTTP.RedirectChain = []RedirectHop{{URL: "http://example.com/", Status: 301, Location: "https://www.example.com/"}}
		}, wwwAbsent, "redirect", "another host"},
		{"no redirect", func(s *TLSScan) {
			s.HTTP.RedirectChain = []RedirectHop{{URL: "http://example.com/", Status: 200}}
		}, wwwAbsent, "redirect", "answered 200 without redirecting"},
		{"port 80 silent", func(s *TLSScan) {
			s.HTTP.RedirectChain, s.HTTP.PlainError, s.HTTP.PlainErrorKind = []RedirectHop{}, "i/o timeout", "timeout"
		}, wwwAbsent, "redirect", "did not answer"},
		{"an untrusted certificate", func(s *TLSScan) { s.Trusted, s.TrustError = false, "x509: certificate signed by unknown authority" },
			wwwAbsent, "certificate", "unknown authority"},
		{"HTTPS redirects to HTTP", func(s *TLSScan) { s.HTTP.StatusCode, s.HTTP.Location = 301, "http://example.com/" },
			wwwAbsent, "https-redirect", "an insecure page"},
		{"www without HTTPS", func(*TLSScan) {}, &PreloadRule{ID: "www", Detail: "no TLS handshake"}, "www", "no TLS handshake"},
	} {
		scan := preloadable()
		c.change(scan)
		check := preloadCheck("example.com", scan, c.www)
		rule := preloadRule(check, c.rule)
		if check.Eligible || rule == nil || rule.Passed || !strings.Contains(rule.Detail, c.detail) {
			t.Errorf("%s: eligible=%v rule=%+v", c.name, check.Eligible, rule)
		}
	}

	// The list asks for the redirect only "if you are listening on port 80".
	scan := preloadable()
	scan.HTTP.RedirectChain, scan.HTTP.PlainError, scan.HTTP.PlainErrorKind = []RedirectHop{}, "connection refused", "refused"
	if check := preloadCheck("example.com", scan, wwwAbsent); !check.Eligible {
		t.Errorf("a closed port 80 is allowed: %+v", check)
	}
	// An HTTPS answer that redirects on HTTPS is allowed, and checked.
	scan = preloadable()
	scan.HTTP.StatusCode, scan.HTTP.Location = 301, "https://example.com/home"
	if check := preloadCheck("example.com", scan, wwwAbsent); !check.Eligible || preloadRule(check, "https-redirect") == nil {
		t.Errorf("an HTTPS redirect on HTTPS: %+v", check)
	}
}

func TestPreloadTakesOnlyARegistrableDomain(t *testing.T) {
	check := preloadCheck("app.example.com", preloadable(), nil)
	if check.Eligible || check.Domain != "example.com" || len(check.Rules) != 1 || check.Rules[0].Passed ||
		!strings.Contains(check.Rules[0].Detail, "submitting example.com") {
		t.Fatalf("subdomain: %+v", check)
	}
	if check := preloadCheck("example.co.uk", preloadable(), wwwAbsent); !check.Eligible {
		t.Errorf("a registrable domain under a two-label suffix: %+v", check)
	}
	if check := preloadCheck("co.uk", preloadable(), nil); check.Eligible || check.Domain != "" {
		t.Errorf("a public suffix: %+v", check)
	}
}

func TestCheckWWW(t *testing.T) {
	notFound := func(context.Context, string) ([]net.IPAddr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "www.example.com", IsNotFound: true}
	}
	if rule := checkWWW(context.Background(), "example.com", "443", notFound); !rule.Passed || !strings.Contains(rule.Detail, "no DNS record") {
		t.Errorf("no www record: %+v", rule)
	}
	failing := func(context.Context, string) ([]net.IPAddr, error) {
		return nil, &net.DNSError{Err: "server misbehaving", Name: "www.example.com", IsTemporary: true}
	}
	if rule := checkWWW(context.Background(), "example.com", "443", failing); rule.Passed {
		t.Errorf("a failed lookup is not a pass: %+v", rule)
	}

	cert, _ := scanTestCert(t, 13, nil)
	addr := tlsListener(t, &tls.Config{Certificates: []tls.Certificate{cert}}, func(*tls.Conn) {})
	_, port := splitAddr(t, addr)
	loopback := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	rule := checkWWW(context.Background(), "example.com", strconv.Itoa(port), loopback)
	if rule.Passed || !strings.Contains(rule.Detail, "not trusted") {
		t.Errorf("a self-signed www: %+v", rule)
	}
	if rule := checkWWW(context.Background(), "example.com", "1", loopback); rule.Passed || !strings.Contains(rule.Detail, "no TLS handshake completed") {
		t.Errorf("a www that does not answer: %+v", rule)
	}
}
