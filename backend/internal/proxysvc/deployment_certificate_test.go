package proxysvc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentCertificateReusesPrivateNameBeforeDNSOrCertbot(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "sites-available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "private.example.invalid"
	certPath, keyPath := writeDeploymentCertificate(t, root, []string{name})
	vhost := "server {\n listen 443 ssl;\n server_name " + name + ";\n ssl_certificate " + certPath + ";\n ssl_certificate_key " + keyPath + ";\n}\n"
	if err := os.WriteFile(filepath.Join(available, "private.conf"), []byte(vhost), 0o640); err != nil {
		t.Fatal(err)
	}
	result, err := New(root, filepath.Join(root, "Caddyfile")).EnsureDeploymentCertificate(context.Background(), []string{name}, nil)
	if err != nil || result.Outcome != CertificateReused || result.CertPath != certPath || result.KeyPath != keyPath {
		t.Fatalf("private certificate reuse = %+v, %v", result, err)
	}
}

func TestDeploymentHTTPDomains(t *testing.T) {
	const name = "wesmokefish-144f0a.100-110-34-31.sslip.io"
	for _, tc := range []struct {
		name      string
		ips       []string
		lookupErr error
		want      string
	}{
		{name: "reported hostname", ips: []string{"100.110.34.31"}, want: "non-public address 100.110.34.31"},
		{name: "private", ips: []string{"192.168.1.2"}, want: "non-public address"},
		{name: "mixed records", ips: []string{"8.8.8.8", "fd7a:115c:a1e0::1"}, want: "non-public address"},
		{name: "public", ips: []string{"8.8.8.8", "2606:4700::1111"}},
		{name: "empty", want: "no DNS addresses"},
		{name: "DNS error", lookupErr: errors.New("DNS unavailable"), want: "DNS unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(ctx context.Context, domain string) ([]net.IPAddr, error) {
				if domain != name {
					t.Fatalf("resolved %q, want frozen release hostname", domain)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("DNS lookup is not bounded")
				}
				var addresses []net.IPAddr
				for _, ip := range tc.ips {
					addresses = append(addresses, net.IPAddr{IP: net.ParseIP(ip)})
				}
				return addresses, tc.lookupErr
			}
			err := validateDeploymentHTTPDomains(context.Background(), []string{name}, lookup)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
