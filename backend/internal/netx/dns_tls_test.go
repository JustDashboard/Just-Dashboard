package netx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// serveDoT answers DNS over TLS on a local port with certificate, and returns
// its address. answer builds each response from the request.
func serveDoT(t *testing.T, certificate tls.Certificate, answer func([]byte) []byte) string {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				for {
					var size [2]byte
					if _, err := io.ReadFull(conn, size[:]); err != nil {
						return
					}
					body := make([]byte, binary.BigEndian.Uint16(size[:]))
					if _, err := io.ReadFull(conn, body); err != nil {
						return
					}
					response := answer(body)
					if _, err := conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(response))), response...)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

// signedAnswer answers A questions with 192.0.2.7, the AD bit and one RRSIG,
// the shape a validating resolver returns to a DO query.
func signedAnswer(req []byte) []byte {
	name, qtype, qend := parseQuestion(req)
	if qtype != typeA {
		return buildResponse(req, qend, qtype, dnsBehavior{})
	}
	_ = name
	resp := buildResponse(req, qend, qtype, dnsBehavior{rdatas: [][]byte{aData("192.0.2.7")}})
	binary.BigEndian.PutUint16(resp[2:], binary.BigEndian.Uint16(resp[2:])|0x0020)
	binary.BigEndian.PutUint16(resp[6:], 2)
	rrsig := make([]byte, 18)
	binary.BigEndian.PutUint16(rrsig, typeA)
	rrsig = append(rrsig, 0, 1, 2, 3)
	resp = append(resp, 0xC0, 0x0C)
	resp = binary.BigEndian.AppendUint16(resp, 46)
	resp = binary.BigEndian.AppendUint16(resp, 1)
	resp = binary.BigEndian.AppendUint32(resp, 60)
	resp = binary.BigEndian.AppendUint16(resp, uint16(len(rrsig)))
	return append(resp, rrsig...)
}

// dotHost stages resolved's global servers and routes each DoT address to a
// local listener; anything else fails to dial.
func dotHost(t *testing.T, status string, routes map[string]string, roots *x509.CertPool) *Service {
	t.Helper()
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "active\n").on("resolvectl status --no-pager", status)
	pointResolvConf(t, "stub")
	prevDial, prevRoots, prevTimeout := dnsTLSDial, dnsTLSRoots, lookupTimeout
	dnsTLSDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		local, ok := routes[address]
		if !ok {
			return nil, errors.New("connection refused")
		}
		var d net.Dialer
		return d.DialContext(ctx, network, local)
	}
	dnsTLSRoots, lookupTimeout = roots, 2*time.Second
	t.Cleanup(func() { dnsTLSDial, dnsTLSRoots, lookupTimeout = prevDial, prevRoots, prevTimeout })
	return testService(t)
}

func TestCheckDNSTLSVerifiesEachConfiguredServer(t *testing.T) {
	certificate, ca := dnsEvidenceCertificate(t)
	stranger, _ := dnsEvidenceCertificate(t)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	good := serveDoT(t, certificate, signedAnswer)
	untrusted := serveDoT(t, stranger, signedAnswer)
	silent := serveDoT(t, certificate, func([]byte) []byte { return []byte{0, 0} })
	status := `Global
         Protocols: +DNSOverTLS DNSSEC=no/unsupported
       DNS Servers: 127.0.0.1#resolver.fixture.example 127.0.0.2#wrong.fixture.example 127.0.0.3
                    127.0.0.4#resolver.fixture.example 127.0.0.5#resolver.fixture.example
Fallback DNS Servers: 127.0.0.6#resolver.fixture.example
`
	s := dotHost(t, status, map[string]string{"127.0.0.1:853": good, "127.0.0.2:853": good, "127.0.0.5:853": untrusted, "127.0.0.6:853": silent}, roots)
	report, err := s.CheckDNSTLS(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DNSTLSCheck{}
	for _, c := range report.Checks {
		by[c.Server] = c
	}
	if c := by["127.0.0.1#resolver.fixture.example"]; c.State != "trusted" || c.Version == "" || c.ChainLength != 2 || len(c.Fingerprint) != 64 || c.NotAfter == nil || c.Scope != "global" || c.Names[0] != "resolver.fixture.example" {
		t.Fatalf("trusted = %+v", c)
	}
	if c := by["127.0.0.2#wrong.fixture.example"]; c.State != "untrusted" || !strings.Contains(c.Error, "not valid for wrong.fixture.example") {
		t.Fatalf("wrong name = %+v", c)
	}
	if c := by["127.0.0.4#resolver.fixture.example"]; c.State != "unreachable" {
		t.Fatalf("unreachable = %+v", c)
	}
	if c := by["127.0.0.5#resolver.fixture.example"]; c.State != "untrusted" || !strings.Contains(c.Error, "authority") {
		t.Fatalf("unknown authority = %+v", c)
	}
	if c := by["127.0.0.6#resolver.fixture.example"]; c.State != "no-answer" || c.Scope != "global fallback" {
		t.Fatalf("silent = %+v", c)
	}
	if len(report.Checks) != 5 || len(report.Omitted) != 1 || report.Omitted[0].Server != "127.0.0.3" || !strings.Contains(report.Omitted[0].Reason, "cannot be authenticated") {
		t.Fatalf("report = %+v", report)
	}

	// A preset is checked only when named, and an address of the caller's own
	// is refused.
	presets, err := s.CheckDNSTLS(context.Background(), []string{"9.9.9.9#dns.quad9.net", "127.0.0.3"})
	if err != nil || len(presets.Checks) != 1 || presets.Checks[0].State != "unreachable" || presets.Checks[0].Scope != "Quad9" || len(presets.Omitted) != 1 {
		t.Fatalf("preset = %+v, %v", presets, err)
	}
	if _, err := s.CheckDNSTLS(context.Background(), []string{"192.0.2.99#probe.example"}); err == nil {
		t.Fatal("an arbitrary server was checked")
	}
}

// A comparison over DNS over TLS asks each destination with an identity over a
// verified session, says which selected destination had none, and reports the
// AD claim and signatures a DO query brought back.
func TestCompareOverTLSWithDNSSECAndOmissions(t *testing.T) {
	certificate, ca := dnsEvidenceCertificate(t)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	var asked atomic.Int32
	good := serveDoT(t, certificate, func(req []byte) []byte { asked.Add(1); return signedAnswer(req) })
	status := "Global\n       DNS Servers: 127.0.0.1#resolver.fixture.example 127.0.0.3\n"
	s := dotHost(t, status, map[string]string{"127.0.0.1:853": good}, roots)
	routeDNS(t, map[string]string{})
	res, err := s.LookupWithOptions(context.Background(), "example.com", "A", LookupOptions{Mode: "compare", Destinations: []string{"127.0.0.1", "127.0.0.3"}, AcknowledgeDisclosure: true, Transport: "tls", DNSSEC: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Answers) != 1 || asked.Load() != 1 {
		t.Fatalf("answers = %+v", res.Answers)
	}
	a := res.Answers[0]
	if a.Error != "" || a.Transport != "tls" || a.TLSName != "resolver.fixture.example" || a.TLSVersion == "" || a.AuthenticatedData == nil || !*a.AuthenticatedData || a.Signatures == nil || *a.Signatures != 1 || a.Answers[0] != "192.0.2.7" {
		t.Fatalf("answer = %+v", a)
	}
	omitted := false
	for _, o := range res.Omitted {
		omitted = omitted || o.Server == "127.0.0.3" && strings.Contains(o.Reason, "no DNS-over-TLS identity")
	}
	if !omitted || !strings.Contains(res.Note, "AD flag is each destination's own claim") || res.Route != "explicit comparison over DNS over TLS" {
		t.Fatalf("result = %+v", res)
	}
	// Presets carry their published identity for the same comparison.
	for _, d := range res.Targets {
		if d.Label == "Quad9" && d.TLSName != "dns.quad9.net" {
			t.Fatalf("preset identity = %+v", d)
		}
	}
	if _, err := s.LookupWithOptions(context.Background(), "example.com", "A", LookupOptions{Mode: "compare", Destinations: []string{"127.0.0.3"}, AcknowledgeDisclosure: true, Transport: "tls"}); err == nil || !strings.Contains(err.Error(), "none of the selected destinations") {
		t.Fatalf("all omitted = %v", err)
	}
	for _, opts := range []LookupOptions{{Transport: "tls"}, {DNSSEC: true}, {Mode: "compare", Destinations: []string{"127.0.0.1"}, AcknowledgeDisclosure: true, Transport: "quic"}} {
		if _, err := s.LookupWithOptions(context.Background(), "example.com", "A", opts); err == nil {
			t.Fatalf("options %+v were accepted", opts)
		}
	}
}

// A certificate that does not verify is the answer's error, and nothing is
// accepted from that session.
func TestCompareOverTLSRefusesAnUntrustedCertificate(t *testing.T) {
	_, ca := dnsEvidenceCertificate(t)
	stranger, _ := dnsEvidenceCertificate(t)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	untrusted := serveDoT(t, stranger, signedAnswer)
	s := dotHost(t, "Global\n       DNS Servers: 127.0.0.1#resolver.fixture.example\n", map[string]string{"127.0.0.1:853": untrusted}, roots)
	res, err := s.LookupWithOptions(context.Background(), "example.com", "A", LookupOptions{Mode: "compare", Destinations: []string{"127.0.0.1"}, AcknowledgeDisclosure: true, Transport: "tls"})
	if err != nil || len(res.Answers) != 1 || len(res.Answers[0].Answers) != 0 || !strings.Contains(res.Answers[0].Error, "authority") {
		t.Fatalf("untrusted = %+v, %v", res, err)
	}
}

// Resolved's default route reaches a public server: a private name nothing
// claims is refused before the native adapter is asked anything.
func TestEffectiveLookupRefusesPrivateNamesOnAPublicDefaultRoute(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-active systemd-resolved", "active\n").on("resolvectl status --no-pager", fixture(t, "dns-resolvectl-status.txt"))
	pointResolvConf(t, "stub")
	previousNative := dnsNativeExecutor
	dnsNativeExecutor = func(context.Context, string, ...string) (string, error) {
		t.Fatal("a refused private name reached the native resolver")
		return "", nil
	}
	t.Cleanup(func() { dnsNativeExecutor = previousNative })
	for _, q := range []struct{ name, rtype string }{{"nas.home.arpa", "A"}, {"printer", "A"}, {"192.168.1.20", "PTR"}} {
		_, err := testService(t).Lookup(t.Context(), q.name, q.rtype)
		var refusal *DNSPolicyRefusal
		if !errors.As(err, &refusal) || refusal.Code != "dns_private_name_public_upstream" || !strings.Contains(refusal.Reason, "203.0.113.53") {
			t.Fatalf("%s = %v", q.name, err)
		}
	}
}
