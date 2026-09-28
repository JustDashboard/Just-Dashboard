package proxysvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// quicResponder answers the deep scan's QUIC probe as a QUIC server must: a
// Version Negotiation packet with the connection IDs swapped back.
func quicResponder(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			p := buf[:n]
			if n < 1200 || p[0]&0x80 == 0 {
				continue
			}
			dcid, rest, _ := quicConnectionID(p[5:])
			scid, _, _ := quicConnectionID(rest)
			answer := []byte{0x80, 0, 0, 0, 0, byte(len(scid))}
			answer = append(answer, scid...)
			answer = append(answer, byte(len(dcid)))
			answer = append(answer, dcid...)
			answer = binary.BigEndian.AppendUint32(answer, 1)
			answer = binary.BigEndian.AppendUint32(answer, 0x5a6a7a8a)
			conn.WriteTo(answer, from)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// deepServer is an HTTPS server on 127.0.0.1 with the given TLS settings,
// sending altSvc when it is not empty.
func deepServer(t *testing.T, config *tls.Config, h2 bool, altSvc string) (string, int) {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if altSvc != "" {
			w.Header().Set("Alt-Svc", altSvc)
		}
		w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = h2
	server.TLS = config
	// Every probe that hangs up after the ServerHello is a handshake error
	// to the server; the test is about what the probe read.
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	return splitAddr(t, server.Listener.Addr().String())
}

func deepFinding(d *DeepScan, id string) *ScanFinding {
	for i := range d.Findings {
		if d.Findings[i].ID == id {
			return &d.Findings[i]
		}
	}
	return nil
}

func TestDeepScanOfAGoServer(t *testing.T) {
	cert, _ := scanTestCert(t, 50, nil)
	quic := quicResponder(t)
	host, port := deepServer(t, &tls.Config{Certificates: []tls.Certificate{cert}},
		true, `h3=":`+strconv.Itoa(quic)+`"; ma=86400`)

	d := DeepScanTLS(context.Background(), host, port, nil)
	if !d.Reachable || d.Where != "here" || d.Address != net.JoinHostPort(host, strconv.Itoa(port)) || d.Connections < 20 {
		t.Fatalf("reachable=%v where=%q address=%q connections=%d error=%q", d.Reachable, d.Where, d.Address, d.Connections, d.Error)
	}
	got := map[string]VersionSuites{}
	for _, v := range d.Versions {
		got[v.Name] = v
	}
	if got["TLS 1.3"].Status != "accepted" || len(got["TLS 1.3"].Suites) != 3 || got["TLS 1.2"].Status != "accepted" {
		t.Errorf("versions: %+v", d.Versions)
	}
	if got["SSL 3.0"].Status != "refused" {
		t.Errorf("SSL 3.0 = %+v", got["SSL 3.0"])
	}
	if d.ALPN == nil || d.ALPN.Negotiated != "h2" || d.ALPN.Site != nil {
		t.Errorf("ALPN = %+v", d.ALPN)
	}
	if d.BrowserGroup != "X25519MLKEM768" || d.BrowserGroupVersion != "TLS 1.3" || len(d.Groups) != len(tls13Groups) {
		t.Errorf("groups: browser %q in %q, %d groups", d.BrowserGroup, d.BrowserGroupVersion, len(d.Groups))
	}
	if len(d.Resumption) != 2 || d.Resumption[0].Status != "resumed" || d.Resumption[0].Version != "TLS 1.3" ||
		d.Resumption[1].Status != "resumed" || d.Resumption[1].Version != "TLS 1.2" {
		t.Errorf("resumption: %+v", d.Resumption)
	}
	if h := d.HTTP3; h == nil || !h.Answered || !h.Advertised || h.Port != quic || h.QUIC == nil ||
		!h.QUIC.Answered || strings.Join(h.QUIC.Versions, " ") != "QUIC v1" {
		t.Errorf("HTTP/3: %+v %+v", h, h.QUIC)
	}
	// Go's server hands its one certificate to anyone: a client naming
	// nothing gets it too, which is the finding.
	if len(d.SNI) != 1 || d.SNI[0].Kind != "unknown" || d.SNI[0].Status != "certificate" || !d.SNI[0].SameAsNamed {
		t.Errorf("SNI: %+v", d.SNI)
	}
	if f := deepFinding(d, "tls.sni.default-certificate"); f == nil || !strings.Contains(f.Detail, "scan.test") {
		t.Errorf("findings: %+v", d.Findings)
	}
	for _, id := range []string{"tls.kex.no-pq", "tls.h3.no-quic", "tls.alpn.no-h2", "tls.resumption.none", "tls.cipher.insecure"} {
		if deepFinding(d, id) != nil {
			t.Errorf("%s should not be found: %+v", id, d.Findings)
		}
	}
}

// HTTP/2 negotiated, or not, is held against the site form's switch when
// the connection reached this server.
func TestNextProtocolsAreHeldAgainstTheSiteForm(t *testing.T) {
	cert, _ := scanTestCert(t, 51, nil)
	h2Host, h2Port := deepServer(t, &tls.Config{Certificates: []tls.Certificate{cert}}, true, "")
	d := DeepScanTLS(context.Background(), h2Host, h2Port, &DeepSite{Name: "app", HTTP2: false})
	if d.ALPN == nil || d.ALPN.Negotiated != "h2" || d.ALPN.Site == nil || deepFinding(d, "tls.alpn.h2-unexpected") == nil {
		t.Errorf("h2 with the switch off: %+v %+v", d.ALPN, d.Findings)
	}
	if d := DeepScanTLS(context.Background(), h2Host, h2Port, &DeepSite{Name: "app", HTTP2: true}); deepFinding(d, "tls.alpn.h2-off") != nil ||
		deepFinding(d, "tls.alpn.h2-unexpected") != nil || deepFinding(d, "tls.alpn.no-h2") != nil {
		t.Errorf("h2 as the form says: %+v", d.Findings)
	}

	h1Host, h1Port := deepServer(t, &tls.Config{Certificates: []tls.Certificate{cert}}, false, "")
	d = DeepScanTLS(context.Background(), h1Host, h1Port, &DeepSite{Name: "app", HTTP2: true})
	f := deepFinding(d, "tls.alpn.h2-off")
	if d.ALPN.Negotiated != "http/1.1" || f == nil || f.Level != "warning" || !strings.Contains(f.Detail, "app") {
		t.Errorf("http/1.1 with the switch on: %+v %+v", d.ALPN, d.Findings)
	}
	d = DeepScanTLS(context.Background(), h1Host, h1Port, nil)
	if deepFinding(d, "tls.alpn.no-h2") == nil || d.HTTP3 == nil || d.HTTP3.Advertised {
		t.Errorf("http/1.1 and no site: %+v", d.Findings)
	}
}

// A server that picks its certificate by name hands its default to a client
// that sends none, which is how scanners learn the names on an address; one
// that refuses such a client leaks nothing.
func TestTheCertificateForNoNameIsReported(t *testing.T) {
	named, _ := scanTestCert(t, 52, nil)
	fallback := certFor(t, "internal.example.test")
	leaky := tlsListener(t, &tls.Config{GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if h.ServerName == "scan.test" {
			return &named, nil
		}
		return &fallback, nil
	}}, func(*tls.Conn) {})
	d := deepScanAs(t, leaky, "scan.test")
	if len(d.SNI) != 2 {
		t.Fatalf("SNI probes: %+v", d.SNI)
	}
	for _, p := range d.SNI {
		if p.Status != "certificate" || p.SameAsNamed || strings.Join(p.Names, " ") != "internal.example.test" {
			t.Errorf("%s: %+v", p.Kind, p)
		}
	}
	if f := deepFinding(d, "tls.sni.default-certificate"); f == nil || !strings.Contains(f.Detail, "internal.example.test") ||
		!strings.Contains(f.Advice, "ssl_reject_handshake on") {
		t.Errorf("findings: %+v", d.Findings)
	}

	strict := tlsListener(t, &tls.Config{GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if h.ServerName == "scan.test" {
			return &named, nil
		}
		return nil, errors.New("no such site")
	}}, func(*tls.Conn) {})
	d = deepScanAs(t, strict, "scan.test")
	for _, p := range d.SNI {
		if p.Status != "refused" {
			t.Errorf("%s: %+v", p.Kind, p)
		}
	}
	if deepFinding(d, "tls.sni.default-certificate") != nil {
		t.Errorf("nothing leaked: %+v", d.Findings)
	}
}

// deepScanAs runs the SNI probes against a local listener under a name, the
// way the deep scan does for a domain that resolves here: the named handshake
// first, then the ones naming nothing and nobody.
func deepScanAs(t *testing.T, addr, name string) *DeepScan {
	t.Helper()
	var dials atomic.Int32
	conn, _, err := dialDeep(context.Background(), addr, name, nil, 0, &dials)
	if err != nil {
		t.Fatal(err)
	}
	state := conn.ConnectionState()
	conn.Close()
	_, port := splitAddr(t, addr)
	d := &DeepScan{Domain: name, Port: port, Reachable: true, Address: addr, Where: "here", Versions: []VersionSuites{}}
	d.SNI = sniProbes(context.Background(), addr, name, &state, &dials)
	d.Findings = deepFindings(d)
	return d
}

// certFor is a self-signed leaf for one name.
func certFor(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(60), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestResumptionWithoutTickets(t *testing.T) {
	cert, _ := scanTestCert(t, 53, nil)
	host, port := deepServer(t, &tls.Config{Certificates: []tls.Certificate{cert}, SessionTicketsDisabled: true}, true, "")
	d := DeepScanTLS(context.Background(), host, port, nil)
	if len(d.Resumption) == 0 || d.Resumption[0].Status != "no-ticket" || d.Resumption[0].Version != "TLS 1.3" {
		t.Fatalf("resumption: %+v", d.Resumption)
	}
	if len(d.Resumption) > 1 && d.Resumption[1].Status != "no-ticket" {
		t.Errorf("TLS 1.2 without tickets: %+v", d.Resumption[1])
	}
	if f := deepFinding(d, "tls.resumption.none"); f == nil || f.Title != "TLS 1.3 sessions are not resumed" {
		t.Errorf("findings: %+v", d.Findings)
	}
}

// RSA key exchange and nothing else leaves no suite with forward secrecy.
func TestRSAKeyExchangeOnlyHasNoForwardSecrecy(t *testing.T) {
	host, port := deepServer(t, &tls.Config{
		Certificates: []tls.Certificate{rsaTestCert(t)}, MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_128_CBC_SHA},
	}, false, "")
	d := DeepScanTLS(context.Background(), host, port, nil)
	for _, v := range d.Versions {
		if v.Name == "TLS 1.2" && (v.Status != "accepted" || len(v.Suites) != 2) {
			t.Fatalf("TLS 1.2 = %+v", v)
		}
	}
	if f := deepFinding(d, "tls.cipher.no-forward-secrecy"); f == nil || f.Level != "warning" {
		t.Fatalf("findings: %+v", d.Findings)
	}
}

// Alt-Svc advertising HTTP/3 where nothing answers QUIC sends every browser
// on a detour first.
func TestHTTP3AdvertisedWithoutQUIC(t *testing.T) {
	cert, _ := scanTestCert(t, 54, nil)
	closed, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp := closed.LocalAddr().(*net.UDPAddr).Port
	closed.Close()
	host, port := deepServer(t, &tls.Config{Certificates: []tls.Certificate{cert}}, true, `h3=":`+strconv.Itoa(udp)+`"; ma=86400, h3-29=":`+strconv.Itoa(udp)+`"`)
	d := DeepScanTLS(context.Background(), host, port, nil)
	if d.HTTP3 == nil || !d.HTTP3.Advertised || d.HTTP3.QUIC == nil || d.HTTP3.QUIC.Answered || d.HTTP3.QUIC.Detail == "" {
		t.Fatalf("HTTP/3: %+v", d.HTTP3)
	}
	if f := deepFinding(d, "tls.h3.no-quic"); f == nil || f.Level != "warning" || !strings.Contains(f.Detail, "UDP "+strconv.Itoa(udp)) {
		t.Errorf("findings: %+v", d.Findings)
	}

	// QUIC answering without an Alt-Svc tells no browser to use it.
	quic := quicResponder(t)
	if got := quicTarget(&HTTP3Result{Answered: true}, quic); got != quic {
		t.Fatalf("an unadvertised website is asked on its own port, got %d", got)
	}
	probe := probeQUIC(context.Background(), net.JoinHostPort("127.0.0.1", strconv.Itoa(quic)))
	d = &DeepScan{Reachable: true, Where: "here", HTTP3: &HTTP3Result{Answered: true, QUIC: probe}}
	if f := deepFinding(&DeepScan{Findings: deepFindings(d)}, "tls.h3.not-advertised"); f == nil || !strings.Contains(f.Advice, strconv.Itoa(quic)) {
		t.Errorf("unadvertised QUIC: %+v", probe)
	}
}

func TestAltSvcIsParsed(t *testing.T) {
	got := parseAltSvc(`h3=":443"; ma=86400, h3-29="alt.example.com:8443"; persist=1, h2="x:1", clear, bad="nowhere"`)
	want := []altService{{"h3", "", 443}, {"h3-29", "alt.example.com", 8443}, {"h2", "x", 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	// Another host's alternative is not this scan's to send packets to.
	if quicTarget(&HTTP3Result{Answered: true, Advertised: true, Host: "alt.example.com", Port: 8443}, 443) != 0 {
		t.Error("an alternative on another host was probed")
	}
	if quicTarget(&HTTP3Result{}, 443) != 0 {
		t.Error("a service that answered no HTTP was probed for HTTP/3")
	}
}

func TestVersionNegotiationMustAnswerTheProbe(t *testing.T) {
	dcid, scid := []byte{1, 2, 3, 4, 5, 6, 7, 8}, []byte{9, 9, 9, 9, 9, 9, 9, 9}
	packet := quicProbePacket(dcid, scid)
	if len(packet) != 1200 || binary.BigEndian.Uint32(packet[1:]) != quicProbeVersion {
		t.Fatalf("probe packet: % x", packet[:24])
	}
	answer := append([]byte{0x80, 0, 0, 0, 0, 8}, scid...)
	answer = append(answer, 8)
	answer = append(answer, dcid...)
	answer = binary.BigEndian.AppendUint32(answer, 1)
	answer = binary.BigEndian.AppendUint32(answer, 0x6b3343cf)
	answer = binary.BigEndian.AppendUint32(answer, 0xff00001d)
	answer = binary.BigEndian.AppendUint32(answer, 0x3a4a5a6a)
	versions, ok := parseVersionNegotiation(answer, dcid, scid)
	if !ok || strings.Join(versions, ", ") != "QUIC v1, QUIC v2, draft-29" {
		t.Fatalf("got %v %v", versions, ok)
	}
	// Someone else's connection IDs are not an answer to this probe.
	if _, ok := parseVersionNegotiation(answer, scid, dcid); ok {
		t.Error("an answer to another probe was taken")
	}
}

func TestAnUnreachableTargetIsSaidSo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port := splitAddr(t, ln.Addr().String())
	ln.Close()
	d := DeepScanTLS(context.Background(), host, port, nil)
	if d.Reachable || d.Error == "" || len(d.Findings) != 0 || d.Connections != 1 {
		t.Fatalf("got %+v", d)
	}
}

func TestDeepFindings(t *testing.T) {
	suite := func(openssl string) SuiteResult {
		for _, s := range suiteCatalogue {
			if s.openssl == openssl {
				return suiteResult(s)
			}
		}
		t.Fatalf("%s is not in the catalogue", openssl)
		return SuiteResult{}
	}
	accepted := func(name, order string, suites ...string) VersionSuites {
		v := VersionSuites{Name: name, Status: "accepted", Complete: true, Order: order}
		for _, s := range suites {
			v.Suites = append(v.Suites, suite(s))
		}
		return v
	}
	refused := func(name string) VersionSuites {
		return VersionSuites{Name: name, Status: "refused", Detail: "The server answered: protocol version not supported.", Suites: []SuiteResult{}}
	}
	ids := func(d *DeepScan) string {
		out := []string{}
		for _, f := range deepFindings(d) {
			out = append(out, f.ID)
		}
		return strings.Join(out, " ")
	}

	// RSA key exchange and nothing else: no forward secrecy anywhere, and
	// the client picks among weak suites.
	rsa := &DeepScan{Reachable: true, Where: "here", Versions: []VersionSuites{
		refused("TLS 1.3"), accepted("TLS 1.2", "client", "AES128-GCM-SHA256", "AES256-SHA"), refused("TLS 1.1"), refused("TLS 1.0"), refused("SSL 3.0"),
	}}
	if got := ids(rsa); got != "tls.cipher.no-forward-secrecy tls.no-13 tls.cipher.weak tls.cipher.client-order.TLS 1.2" {
		t.Errorf("RSA only: %s", got)
	}

	legacy := &DeepScan{Reachable: true, Where: "elsewhere", Address: "198.51.100.7:443", DHBits: 1024, Versions: []VersionSuites{
		accepted("TLS 1.3", "server", "TLS_AES_128_GCM_SHA256"),
		accepted("TLS 1.2", "server", "ECDHE-RSA-AES128-GCM-SHA256", "RC4-SHA", "DHE-RSA-AES128-GCM-SHA256"),
		refused("TLS 1.1"), accepted("TLS 1.0", "server", "ECDHE-RSA-AES128-SHA"), accepted("SSL 3.0", "", "RC4-SHA"),
	}}
	findings := deepFindings(legacy)
	if got := ids(legacy); got != "tls.ssl3 tls.cipher.insecure tls.old-protocol.TLS 1.0 tls.kex.weak-dh tls.cipher.weak" {
		t.Errorf("legacy: %s", got)
	}
	if !strings.HasPrefix(findings[0].Advice, "198.51.100.7:443 is not this server") {
		t.Errorf("advice for another host's server: %q", findings[0].Advice)
	}
	if findings[1].Title != "1 insecure cipher suite is accepted" || !strings.Contains(findings[1].Detail, "RC4-SHA (RC4, broken)") {
		t.Errorf("insecure: %+v", findings[1])
	}

	groups := func(pq string) []GroupResult {
		out := []GroupResult{}
		for _, g := range tls13Groups {
			status := "accepted"
			if g.postQuantum {
				status = pq
			}
			out = append(out, GroupResult{ID: g.id, Name: g.name, PostQuantum: g.postQuantum, Status: status})
		}
		return out
	}
	modern := func() *DeepScan {
		return &DeepScan{Reachable: true, Where: "here", Versions: []VersionSuites{
			accepted("TLS 1.3", "server", "TLS_AES_128_GCM_SHA256"), accepted("TLS 1.2", "server", "ECDHE-RSA-AES128-GCM-SHA256"),
			refused("TLS 1.1"), refused("TLS 1.0"), refused("SSL 3.0"),
		}}
	}
	d := modern()
	d.Groups, d.BrowserGroup = groups("refused"), "X25519"
	if got := ids(d); got != "tls.kex.no-pq" {
		t.Errorf("no post-quantum: %s", got)
	}
	d.Groups = groups("accepted")
	if got := ids(d); got != "tls.kex.pq-not-preferred" {
		t.Errorf("post-quantum not chosen: %s", got)
	}
	d.BrowserGroup = "X25519MLKEM768"
	if got := ids(d); got != "" {
		t.Errorf("post-quantum chosen: %s", got)
	}
	d.Groups = groups("unknown")
	d.BrowserGroup = ""
	if got := ids(d); got != "" {
		t.Errorf("a probe that heard nothing is no finding: %s", got)
	}
	if got := ids(&DeepScan{Error: "refused"}); got != "" {
		t.Errorf("unreachable: %s", got)
	}
}

func TestSiteForName(t *testing.T) {
	site := func(name string, enabled bool, listen []string, names ...string) VHost {
		return VHost{Name: name, Kind: KindNginx, Enabled: enabled, Listen: listen, ServerNames: names}
	}
	tls443 := []string{"443 ssl", "[::]:443 ssl"}
	vhosts := []VHost{
		site("wild", true, tls443, "*.example.com"),
		site("deeper", true, tls443, "*.app.example.com"),
		site("exact", true, tls443, "app.example.com", "www.app.example.com"),
		site("off", false, tls443, "off.example.com"),
		site("plain", true, []string{"80"}, "plain.example.com"),
		site("alt", true, []string{"127.0.0.1:8443 ssl"}, "alt.example.com"),
		site("tail", true, tls443, "mail.*"),
		site("dot", true, tls443, ".dot.example"),
		site("regex", true, tls443, "~^re\\d+\\.example\\.com$"),
		site("quic", true, []string{"443 quic reuseport"}, "quic.example.com"),
	}
	for _, c := range []struct {
		name string
		port int
		want string
	}{
		{"app.example.com", 443, "exact"},
		{"APP.example.com.", 443, "exact"},
		{"x.app.example.com", 443, "deeper"},
		{"other.example.com", 443, "wild"},
		{"example.com", 443, ""},
		{"off.example.com", 443, "wild"},
		{"plain.example.com", 443, "wild"},
		{"alt.example.com", 8443, "alt"},
		{"alt.example.com", 443, "wild"},
		{"mail.example.org", 443, "tail"},
		{"dot.example", 443, "dot"},
		{"a.dot.example", 443, "dot"},
		{"re1.example.com", 443, "wild"},
		{"quic.example.com", 8443, ""},
	} {
		got := ""
		if v := SiteForName(vhosts, c.name, c.port); v != nil {
			got = v.Name
		}
		if got != c.want {
			t.Errorf("%s:%d = %q, want %q", c.name, c.port, got, c.want)
		}
	}
	for listen, want := range map[string]int{"443": 443, "*:8443": 8443, "127.0.0.1:443": 443, "[::]:443": 443, "[::1]": 80, "127.0.0.1": 80, "localhost": 80, "unix:/run/x.sock": 0} {
		if got := deepListenPort(listen); got != want {
			t.Errorf("deepListenPort(%q) = %d, want %d", listen, got, want)
		}
	}
}
