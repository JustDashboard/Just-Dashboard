package dbx

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startFake listens on loopback, runs greet on each connection to play the
// protocol's part before TLS, and then serves TLS with the certificate.
func startFake(t *testing.T, certPEM, keyPEM []byte, greet func(net.Conn) bool) string {
	t.Helper()
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
				if greet != nil && !greet(conn) {
					return
				}
				secured := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{pair}})
				if secured.Handshake() == nil {
					_, _ = io.Copy(io.Discard, secured)
				}
			}()
		}
	}()
	return listener.Addr().String()
}

// What a client sees is what the server presents, read in each engine's way
// into TLS.
func TestServedCertificateIsReadInEachEnginesWay(t *testing.T) {
	ca, caPEM, _ := mustCA(t)
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(file, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue([]string{"127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	postgres := func(conn net.Conn) bool {
		request := make([]byte, 8)
		if _, err := io.ReadFull(conn, request); err != nil || request[4] != 0x04 || request[5] != 0xd2 {
			return false
		}
		_, err := conn.Write([]byte("S"))
		return err == nil
	}
	mysql := func(conn net.Conn) bool {
		greeting := []byte("\x0a8.4.0\x00")
		if _, err := conn.Write(append([]byte{byte(len(greeting)), 0, 0, 0}, greeting...)); err != nil {
			return false
		}
		request := make([]byte, 36)
		if _, err := io.ReadFull(conn, request); err != nil {
			return false
		}
		return request[0] == 32 && request[3] == 1 && request[5]&0x08 != 0
	}
	for _, c := range []struct {
		driver Driver
		greet  func(net.Conn) bool
	}{{DriverPostgres, postgres}, {DriverMySQL, mysql}, {DriverMongo, nil}, {DriverRedis, nil}} {
		addr := startFake(t, leaf.CertPEM, leaf.KeyPEM, c.greet)
		cert, err := ServedCertificate(context.Background(), c.driver, addr, file)
		if err != nil {
			t.Errorf("%s: %v", c.driver, err)
			continue
		}
		if got := strings.ToUpper(cert.SerialNumber.Text(16)); got != leaf.Serial {
			t.Errorf("%s: served serial %s, issued %s", c.driver, got, leaf.Serial)
		}
	}
}

func TestServedCertificateIsRefusedFromAnotherAuthorityOrNoTLS(t *testing.T) {
	ca, _, _ := mustCA(t)
	_, otherPEM, _ := mustCA(t)
	file := filepath.Join(t.TempDir(), "other.pem")
	if err := os.WriteFile(file, otherPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue([]string{"127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	addr := startFake(t, leaf.CertPEM, leaf.KeyPEM, nil)
	if _, err := ServedCertificate(context.Background(), DriverRedis, addr, file); err == nil {
		t.Error("a certificate from another authority was accepted")
	}
	refusing := startFake(t, leaf.CertPEM, leaf.KeyPEM, func(conn net.Conn) bool {
		_, _ = io.ReadFull(conn, make([]byte, 8))
		_, _ = conn.Write([]byte("N"))
		return false
	})
	if _, err := ServedCertificate(context.Background(), DriverPostgres, refusing, file); err == nil ||
		!strings.Contains(err.Error(), "does not offer TLS") {
		t.Errorf("a server that declined TLS gave %v", err)
	}
	if _, err := ServedCertificate(context.Background(), DriverRedis, addr, filepath.Join(t.TempDir(), "gone.pem")); err == nil {
		t.Error("an unreadable authority file was papered over")
	}
}
