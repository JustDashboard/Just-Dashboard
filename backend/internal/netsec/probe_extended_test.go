package netsec

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every new target-taking tool carries the same guard as the existing probes:
// a value that is not a hostname never reaches a dial or a command line.
func TestExtendedToolsRejectBadTargets(t *testing.T) {
	s := New()
	ctx := context.Background()
	bad := "-i example.com"
	if _, err := s.DNSAuthority(ctx, bad); err == nil {
		t.Error("DNSAuthority accepted a bad target")
	}
	if _, err := s.BannerGrab(ctx, bad, 22); err == nil {
		t.Error("BannerGrab accepted a bad target")
	}
	if _, err := s.SSHScan(ctx, bad, 22); err == nil {
		t.Error("SSHScan accepted a bad target")
	}
	if _, err := s.STARTTLSCheck(ctx, bad, 25, "smtp"); err == nil {
		t.Error("STARTTLSCheck accepted a bad target")
	}
	if _, err := s.TLSSurvey(ctx, bad, 443); err == nil {
		t.Error("TLSSurvey accepted a bad target")
	}
	if _, err := s.MXCheck(ctx, bad); err == nil {
		t.Error("MXCheck accepted a bad target")
	}
	if _, err := s.HTTPSecurity(ctx, bad, 443, "auto"); err == nil {
		t.Error("HTTPSecurity accepted a bad target")
	}
	if _, err := s.SiteAudit(ctx, bad, 443); err == nil {
		t.Error("SiteAudit accepted a bad target")
	}
	if _, err := s.DNSBLCheck(ctx, "example.com"); err == nil {
		t.Error("DNSBLCheck accepted a hostname")
	}
	if _, err := s.ASNLookup(ctx, "example.com"); err == nil {
		t.Error("ASNLookup accepted a hostname")
	}
	if _, err := s.MXCheck(ctx, "8.8.8.8"); err == nil {
		t.Error("MXCheck accepted an IP address")
	}
	if _, err := s.BannerGrab(ctx, "127.0.0.1", 0); err == nil {
		t.Error("BannerGrab accepted port 0")
	}
	if _, err := s.STARTTLSCheck(ctx, "127.0.0.1", 25, "gopher"); err == nil {
		t.Error("STARTTLSCheck accepted an unknown protocol")
	}
	// Implicit-TLS ports never upgrade; sending STARTTLS there is a protocol
	// error, and the message must say where to go instead.
	if _, err := s.STARTTLSCheck(ctx, "127.0.0.1", 465, "smtp"); err == nil {
		t.Error("STARTTLSCheck accepted implicit-TLS port 465")
	} else if !strings.Contains(err.Error(), "TLS tool") {
		t.Errorf("465 refusal does not point at the TLS tool: %v", err)
	}
}

func TestBannerGrabReadsABanner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			defer c.Close()
			fmt.Fprintf(c, "SSH-2.0-test-server\r\n")
			time.Sleep(2 * time.Second)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	res, err := New().BannerGrab(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !strings.Contains(res.Output, "SSH-2.0-test-server") {
		t.Fatalf("banner not read: %+v", res)
	}
}

func TestBannerGrabRefusedReadsAsRefused(t *testing.T) {
	// A closed port on loopback refuses fast, so this stays hermetic.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	res, err := New().BannerGrab(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("refused port reported ok: %+v", res)
	}
	if !strings.Contains(res.Output, "refused") {
		t.Fatalf("no refused explanation: %q", res.Output)
	}
}

func TestTLSSurveyAgainstLocalServer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	res, err := New().TLSSurvey(context.Background(), host, port)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || len(res.Records) == 0 {
		t.Fatalf("survey found nothing: %+v", res)
	}
	// The survey names every version it tried, offered or refused — a row per
	// version is the shape the UI renders.
	for _, v := range []string{"TLS 1.0", "TLS 1.1", "TLS 1.2", "TLS 1.3"} {
		if !strings.Contains(res.Output, v) {
			t.Errorf("survey says nothing about %s:\n%s", v, res.Output)
		}
	}
}

func TestHTTPSecurityGradesHeaders(t *testing.T) {
	hardened := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
	}))
	defer hardened.Close()
	host, port := hostPort(t, hardened.URL)
	res, err := New().HTTPSecurity(context.Background(), host, port, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("not ok: %+v", res)
	}
	if !strings.Contains(res.Output, "5 of 5 applicable headers hardened") {
		t.Errorf("fully hardened server did not score 5/5:\n%s", res.Output)
	}
	if len(res.Records) != 0 {
		t.Errorf("fully hardened server has findings: %v", res.Records)
	}

	bare := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer bare.Close()
	bhost, bport := hostPort(t, bare.URL)
	bres, err := New().HTTPSecurity(context.Background(), bhost, bport, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bres.Output, "FAIL") {
		t.Errorf("bare server drew no FAIL:\n%s", bres.Output)
	}
	if len(bres.Records) == 0 {
		t.Error("bare server drew no findings")
	}
}

// startFakeSMTP serves one connection: greeting, EHLO multiline, STARTTLS,
// then a real TLS handshake with a throwaway certificate.
func startFakeSMTP(t *testing.T) (string, int) {
	t.Helper()
	cert := selfSignedCert(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprintf(c, "220 fake.test ESMTP\r\n")
		buf := make([]byte, 1024)
		for {
			n, err := c.Read(buf)
			if err != nil || n == 0 {
				return
			}
			line := strings.ToUpper(strings.TrimSpace(string(buf[:n])))
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprintf(c, "250-fake.test\r\n250 STARTTLS\r\n")
			case strings.HasPrefix(line, "STARTTLS"):
				fmt.Fprintf(c, "220 2.0.0 Ready to start TLS\r\n")
				tlsConn := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
				if herr := tlsConn.Handshake(); herr != nil {
					return
				}
				tlsConn.Close()
				return
			}
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"fake.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(
		pemEncode("CERTIFICATE", der),
		pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func pemEncode(kind string, der []byte) []byte {
	var sb strings.Builder
	sb.WriteString("-----BEGIN " + kind + "-----\n")
	enc := base64.StdEncoding.EncodeToString(der)
	for i := 0; i < len(enc); i += 64 {
		end := i + 64
		if end > len(enc) {
			end = len(enc)
		}
		sb.WriteString(enc[i:end] + "\n")
	}
	sb.WriteString("-----END " + kind + "-----\n")
	return []byte(sb.String())
}

func TestSTARTTLSAgainstFakeSMTP(t *testing.T) {
	host, port := startFakeSMTP(t)
	res, err := New().STARTTLSCheck(context.Background(), host, port, "smtp")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("not ok: output=%q error=%q", res.Output, res.Error)
	}
	for _, want := range []string{"Upgrade accepted", "fake.test"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output missing %q:\n%s", want, res.Output)
		}
	}
}

func TestSiteAuditMergesThreeSections(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	res, err := New().SiteAudit(context.Background(), host, port)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("not ok: output=%q error=%q", res.Output, res.Error)
	}
	for _, want := range []string{"── HTTP ──", "── Certificate ──", "── Headers ──"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("audit missing section %q:\n%s", want, res.Output)
		}
	}
}

// The reversed forms blocklists are queried under.
func TestReverseForDNSBL(t *testing.T) {
	if got := reverseForDNSBL(net.ParseIP("1.2.3.4")); got != "4.3.2.1" {
		t.Errorf("v4 reversed to %q", got)
	}
	if got := reverseForDNSBL(net.ParseIP("::1")); !strings.HasPrefix(got, "1.0.0.0.0.0.0.0") {
		t.Errorf("v6 reversed to %q", got)
	}
}

func TestParseMaxAge(t *testing.T) {
	if got := parseMaxAge("max-age=31536000; includeSubDomains"); got != 31536000 {
		t.Errorf("got %d", got)
	}
	if got := parseMaxAge("max-age=100"); got != 100 {
		t.Errorf("got %d", got)
	}
	if got := parseMaxAge("includeSubDomains"); got != -1 {
		t.Errorf("missing max-age parsed as %d", got)
	}
}
