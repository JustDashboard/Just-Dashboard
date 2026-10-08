package netx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

// Every native command and resolver runs in a child with new network/mount
// namespaces and a private /etc, /run/systemd and D-Bus. No host service or
// production resolver setting is changed, including on a failed assertion.
func TestDNSEvidenceNativeDisposableResolver(t *testing.T) {
	if os.Getenv("JD_DNS_EVIDENCE_FIXTURE") == "1" {
		dnsEvidenceNativeFixture(t)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("native resolver fixture requires disposable namespace privileges")
	}
	for _, tool := range []string{"busctl", "dbus-daemon", "ip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("native fixture tool unavailable: %s", tool)
		}
	}
	if _, err := os.Stat("/usr/lib/systemd/systemd-resolved"); err != nil {
		t.Skip("native systemd-resolved executable unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDNSEvidenceNativeDisposableResolver$", "-test.v")
	cmd.Env = append(os.Environ(), "JD_DNS_EVIDENCE_FIXTURE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET | unix.CLONE_NEWNS}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("namespace creation is unavailable")
		}
		t.Fatalf("native resolver fixture: %v\n%s", err, out)
	}
	t.Log(string(out))
}

func dnsEvidenceNativeFixture(t *testing.T) {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	etc := filepath.Join(dir, "etc")
	run := filepath.Join(dir, "run")
	dbus := filepath.Join(run, "fixturebus")
	for _, path := range []string{etc, filepath.Join(etc, "systemd", "resolved.conf.d"), filepath.Join(etc, "dnssec-trust-anchors.d"), filepath.Join(etc, "ssl", "certs"), filepath.Join(run, "resolve"), dbus} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"passwd", "group", "nsswitch.conf", "machine-id"} {
		raw, err := os.ReadFile("/etc/" + name)
		if err == nil {
			if err = os.WriteFile(filepath.Join(etc, name), raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(etc, "hosts"), []byte("127.0.0.1 localhost\n203.0.113.99 secret.corp.example\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(etc, "resolv.conf"), []byte("nameserver 127.0.0.53\n"), 0644); err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	anchor := "corp.example IN DNSKEY 257 3 15 " + base64.StdEncoding.EncodeToString(public) + "\n"
	if err = os.WriteFile(filepath.Join(etc, "dnssec-trust-anchors.d", "fixture.positive"), []byte(anchor), 0644); err != nil {
		t.Fatal(err)
	}
	certificate, ca := dnsEvidenceCertificate(t)
	if err = os.WriteFile(filepath.Join(etc, "ssl", "certs", "ca-certificates.crt"), ca, 0644); err != nil {
		t.Fatal(err)
	}
	conf := "[Resolve]\nDNS=127.0.0.3\nFallbackDNS=\nDomains=~.\nDNSSEC=yes\nDNSOverTLS=yes\nLLMNR=no\nMulticastDNS=no\nDNSStubListener=no\nCache=yes\n"
	if err = os.WriteFile(filepath.Join(etc, "systemd", "resolved.conf"), []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}
	for _, mount := range [][2]string{{etc, "/etc"}, {run, "/run/systemd"}} {
		if err = unix.Mount(mount[0], mount[1], "", unix.MS_BIND, ""); err != nil {
			t.Fatal(err)
		}
	}
	account, err := user.Lookup("systemd-resolve")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	if err = os.Chown("/run/systemd/resolve", uid, gid); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	execute := func(ctx context.Context, name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		var out dnsBoundedBuffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		_, err := hostexec.RunGroup(ctx, cmd, 100*time.Millisecond)
		if out.exceeded {
			return "", fmt.Errorf("fixture output exceeded bound")
		}
		if err != nil {
			return out.String(), fmt.Errorf("%s", strings.TrimSpace(out.String()))
		}
		return out.String(), nil
	}
	if out, err := execute(ctx, "ip", "link", "set", "lo", "up"); err != nil {
		t.Fatalf("loopback: %s %v", out, err)
	}
	if out, err := execute(ctx, "ip", "link", "add", "dnspriv0", "type", "dummy"); err != nil {
		t.Fatalf("private link: %s %v", out, err)
	}
	if out, err := execute(ctx, "ip", "link", "set", "dnspriv0", "up"); err != nil {
		t.Fatalf("private link: %s %v", out, err)
	}
	for _, args := range [][]string{{"address", "add", "192.0.2.53/24", "dev", "dnspriv0"}, {"-6", "address", "add", "fdce::53/64", "dev", "dnspriv0", "nodad"}} {
		if out, err := execute(ctx, "ip", args...); err != nil {
			t.Fatalf("fixture addresses: %s %v", out, err)
		}
	}
	address := "unix:path=/run/systemd/fixturebus/bus"
	busConf := `<busconfig><type>system</type><listen>` + address + `</listen><policy context="default"><allow user="*"/><allow own="*"/><allow send_destination="*"/><allow receive_sender="*"/></policy></busconfig>`
	busPath := filepath.Join(dbus, "config")
	if err = os.WriteFile(busPath, []byte(busConf), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address)
	t.Setenv("SSL_CERT_FILE", "/etc/ssl/certs/ca-certificates.crt")
	start := func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(name, args...)
		cmd.Env = os.Environ()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		log, err := os.Create(filepath.Join(dir, filepath.Base(name)+".log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); _ = cmd.Wait(); _ = log.Close() })
		return cmd
	}
	start("dbus-daemon", "--config-file="+busPath, "--nofork", "--nopidfile")
	var publicQueries, privateQueries, privateIPv6Queries atomic.Int32
	dnsEvidenceServeTLS(t, "127.0.0.3:853", certificate, func(req []byte) []byte { publicQueries.Add(1); return dnsEvidenceSignedResponse(req, key, false) })
	dnsEvidenceServeTLS(t, "127.0.0.2:853", certificate, func(req []byte) []byte {
		privateQueries.Add(1)
		name, _, _ := parseQuestion(req)
		return dnsEvidenceSignedResponse(req, key, strings.HasPrefix(name, "bogus."))
	})
	dnsEvidenceServeTLS(t, "[fdce::53]:853", certificate, func(req []byte) []byte {
		privateIPv6Queries.Add(1)
		return dnsEvidenceSignedResponse(req, key, false)
	})
	start("/usr/lib/systemd/systemd-resolved")
	var owner string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		owner, err = dnsBusOwner(ctx, execute)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if owner == "" {
		raw, _ := os.ReadFile(filepath.Join(dir, "systemd-resolved.log"))
		t.Fatalf("fixture owner unavailable: %v\n%s", err, raw)
	}
	// Native policy changes are confined to the fixture's private D-Bus owner.
	for _, args := range [][]string{{"dns", "dnspriv0", "127.0.0.2#resolver.fixture.example"}, {"domain", "dnspriv0", "~corp.example"}, {"default-route", "dnspriv0", "no"}, {"dnsovertls", "dnspriv0", "yes"}, {"dnssec", "dnspriv0", "yes"}} {
		if out, err := execute(ctx, "resolvectl", args...); err != nil {
			t.Fatalf("fixture policy %v: %s %v", args, out, err)
		}
	}
	s := &Service{}
	for _, kind := range []string{"A", "AAAA"} {
		r, err := s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: "secret.corp.example", Type: kind, ExpectedInterface: "dnspriv0"}, execute)
		if err != nil || r.Error != "" || r.Route.State != "measured" || r.Transport.State != "encrypted" || r.Trust.State != "native_policy_validated" || r.DNSSEC.State != "validated" || len(r.Answers) != 1 || r.Answers[0] == "203.0.113.99" {
			raw, _ := os.ReadFile(filepath.Join(dir, "systemd-resolved.log"))
			t.Fatalf("native %s evidence: %+v %v\n%s", kind, r, err, raw)
		}
		if publicQueries.Load() != 0 || privateQueries.Load() == 0 {
			t.Fatalf("private name leaked to default scope: public=%d private=%d", publicQueries.Load(), privateQueries.Load())
		}
	}
	if out, err := execute(ctx, "resolvectl", "dns", "dnspriv0", "fdce::53#resolver.fixture.example"); err != nil {
		t.Fatalf("fixture IPv6 resolver: %s %v", out, err)
	}
	ipv6Report, err := s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: "ipv6.corp.example", Type: "AAAA", ExpectedInterface: "dnspriv0"}, execute)
	if err != nil || ipv6Report.Error != "" || ipv6Report.Transport.State != "encrypted" || ipv6Report.DNSSEC.State != "validated" || privateIPv6Queries.Load() == 0 || publicQueries.Load() != 0 || ipv6Report.UpstreamFamily != "not_measured" {
		t.Fatalf("native IPv6-scope evidence: %+v %v", ipv6Report, err)
	}
	if out, err := execute(ctx, "resolvectl", "dns", "dnspriv0", "127.0.0.2#resolver.fixture.example"); err != nil {
		t.Fatalf("fixture restore IPv4 resolver: %s %v", out, err)
	}
	r, err := s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: "bogus.corp.example", Type: "A"}, execute)
	if err != nil || r.Error == "" || r.DNSSEC.State != "validation_failed" || len(r.Answers) != 0 || publicQueries.Load() != 0 {
		t.Fatalf("bogus data or fallback accepted: %+v %v public=%d", r, err, publicQueries.Load())
	}
	// Wrong TLS identity must fail through the native strict policy, with no
	// cleartext/default-server attempt or encryption claim.
	if out, err := execute(ctx, "resolvectl", "dns", "dnspriv0", "127.0.0.2#wrong.fixture.example"); err != nil {
		t.Fatalf("fixture wrong identity: %s %v", out, err)
	}
	r, err = s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: "tlsfail.corp.example", Type: "A"}, execute)
	if err != nil || r.Error == "" || r.Transport.State != "unknown" || r.Trust.State != "unknown" || publicQueries.Load() != 0 {
		t.Fatalf("certificate failure claimed encrypted success: %+v %v", r, err)
	}
	if out, err := execute(ctx, "ip", "link", "set", "dnspriv0", "down"); err != nil {
		t.Fatalf("fixture inactive scope: %s %v", out, err)
	}
	r, err = s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: "inactive.corp.example", Type: "A"}, execute)
	if err != nil || r.Error == "" || !strings.Contains(r.Error, "scope is unavailable") || publicQueries.Load() != 0 || len(r.Policy) != 1 || r.Policy[0].ActiveDNS {
		t.Fatalf("inactive declared scope fell back: %+v %v default queries=%d", r, err, publicQueries.Load())
	}
}

func dnsEvidenceCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Disposable DNS fixture CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	root, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "resolver.fixture.example"}, DNSNames: []string{"resolver.fixture.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root})
}
func dnsEvidenceServeTLS(t *testing.T, address string, certificate tls.Certificate, answer func([]byte) []byte) {
	t.Helper()
	listener, err := tls.Listen("tcp", address, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
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
				_ = conn.SetDeadline(time.Now().Add(25 * time.Second))
				for {
					var header [2]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						return
					}
					body := make([]byte, binary.BigEndian.Uint16(header[:]))
					if _, err := io.ReadFull(conn, body); err != nil {
						return
					}
					response := answer(body)
					frame := binary.BigEndian.AppendUint16(nil, uint16(len(response)))
					frame = append(frame, response...)
					if _, err := conn.Write(frame); err != nil {
						return
					}
				}
			}()
		}
	}()
}
func dnsEvidenceSignedResponse(req []byte, key ed25519.PrivateKey, bogus bool) []byte {
	name, kind, qend := parseQuestion(req)
	body := []byte{192, 0, 2, 7}
	if kind == 28 {
		body = net.ParseIP("2001:db8::7").To16()
	}
	dnskey := append([]byte{1, 1, 3, 15}, key.Public().(ed25519.PublicKey)...)
	if kind == 48 {
		body = dnskey
	}
	if kind != 1 && kind != 28 && kind != 48 {
		return buildResponse(req, qend, kind, dnsBehavior{rcode: 3})
	}
	rr := dnsEvidenceRR(name, kind, body)
	var tag uint32
	for i, b := range dnskey {
		if i&1 == 0 {
			tag += uint32(b) << 8
		} else {
			tag += uint32(b)
		}
	}
	tag += (tag >> 16) & 65535
	signature := binary.BigEndian.AppendUint16(nil, kind)
	signature = append(signature, 15, byte(len(strings.Split(name, "."))))
	signature = binary.BigEndian.AppendUint32(signature, 60)
	signature = binary.BigEndian.AppendUint32(signature, uint32(time.Now().Add(time.Hour).Unix()))
	signature = binary.BigEndian.AppendUint32(signature, uint32(time.Now().Add(-time.Minute).Unix()))
	signature = binary.BigEndian.AppendUint16(signature, uint16(tag))
	for _, label := range []string{"corp", "example"} {
		signature = append(signature, byte(len(label)))
		signature = append(signature, label...)
	}
	signature = append(signature, 0)
	signed := ed25519.Sign(key, append(append([]byte{}, signature...), rr...))
	if bogus {
		signed[0] ^= 1
	}
	signature = append(signature, signed...)
	response := buildResponse(req, qend, kind, dnsBehavior{})
	binary.BigEndian.PutUint16(response[2:], 0x8580)
	binary.BigEndian.PutUint16(response[6:], 2)
	return append(append(response, rr...), dnsEvidenceRR(name, 46, signature)...)
}
