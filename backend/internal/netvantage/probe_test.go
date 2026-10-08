package netvantage

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func nativeDNS(t *testing.T, address net.IP) *net.Resolver {
	t.Helper()
	udp, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { udp.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, peer, e := udp.ReadFrom(buf)
			if e != nil {
				return
			}
			data := append([]byte(nil), buf[:n]...)
			if len(data) < 17 {
				continue
			}
			end := 12
			for end < len(data) && data[end] != 0 {
				end += int(data[end]) + 1
			}
			end += 5
			if end > len(data) {
				continue
			}
			answer := append([]byte(nil), data[:2]...)
			answer = append(answer, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0)
			answer = append(answer, data[12:end]...)
			typ := binary.BigEndian.Uint16(data[end-4 : end-2])
			raw := address.To4()
			if typ == 28 {
				raw = address.To16()
			}
			if raw == nil {
				raw = []byte{192, 0, 2, 1}
			}
			answer = append(answer, 0xc0, 0x0c, byte(typ>>8), byte(typ), 0, 1, 0, 0, 0, 30, 0, byte(len(raw)))
			answer = append(answer, raw...)
			udp.WriteTo(answer, peer)
		}
	}()
	// The controlled resolver is test-local. Production always uses the
	// rootless agent's native resolver and provides no DNS server argument.
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", udp.LocalAddr().String())
	}}
	return resolver
}
func nativeTLS(t *testing.T, family string) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture.private"}, DNSNames: []string{"fixture.private"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if family == "inet6" {
		server.Listener.Close()
		server.Listener, e = net.Listen("tcp6", "[::1]:0")
		if e != nil {
			t.Skipf("controlled IPv6 loopback is unavailable: %v", e)
		}
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, pool
}
func probeJob(server *httptest.Server, family string) Job {
	address := "127.0.0.1"
	if family == "inet6" {
		address = "::1"
	}
	port := server.Listener.Addr().(*net.TCPAddr).Port
	r := Request{VantageID: strings.Repeat("a", 32), ScopeID: "service", Family: family, Port: port, TLS: true}
	return Job{Version: 1, ID: strings.Repeat("b", 32), VantageID: r.VantageID, Nonce: strings.Repeat("c", 32), Request: r, Scope: Scope{ID: "service", Target: "fixture.private", Addresses: []string{address}, Ports: []int{port}, Families: []string{family}}, IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(45 * time.Second).UTC()}
}
func TestNativeDNSPinnedTCPAndVerifiedTLSBothFamilies(t *testing.T) {
	for _, family := range []string{"inet", "inet6"} {
		t.Run(family, func(t *testing.T) {
			server, pool := nativeTLS(t, family)
			job := probeJob(server, family)
			resolver := nativeDNS(t, net.ParseIP(job.Scope.Addresses[0]))
			result := probe(context.Background(), job, resolver, pool)
			if result.Stages[0].State != "resolved" || result.Stages[1].State != "connected" || result.Stages[2].State != "verified" || result.Address != job.Scope.Addresses[0] || result.SourceAddress == "" || result.Certificate == nil || len(result.Certificate.SHA256) != 64 {
				t.Fatalf("native DNS/TCP/TLS scope lost %#v", result)
			}
			if e := validateResult(*result, job.Scope, job.IssuedAt, job.ExpiresAt, time.Now()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestDNSRebindingOutsideEnrolledAddressesStopsBeforeTCP(t *testing.T) {
	server, pool := nativeTLS(t, "inet")
	job := probeJob(server, "inet")
	resolver := nativeDNS(t, net.ParseIP("127.0.0.2"))
	result := probe(context.Background(), job, resolver, pool)
	if result.Address != "" || result.Stages[0].State != "refused" || result.Stages[1].State != "skipped" || result.Stages[2].State != "skipped" {
		t.Fatalf("forbidden DNS answer dialled %#v", result)
	}
}
func TestTLSVerificationFailureRemainsMeasuredFailure(t *testing.T) {
	server, _ := nativeTLS(t, "inet")
	job := probeJob(server, "inet")
	job.Scope.Target = "127.0.0.1"
	result := Probe(context.Background(), job)
	if result.Stages[0].State != "not_applicable" || result.Stages[1].State != "connected" || result.Stages[2].State != "failed" || result.Certificate != nil {
		t.Fatalf("untrusted certificate certified %#v", result)
	}
}
