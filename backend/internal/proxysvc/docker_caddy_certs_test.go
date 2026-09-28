package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The name ensureDockerCaddyCertificate keeps a copy under.
func caddyEvidenceName(domain string) string {
	return "caddy-" + strings.TrimPrefix(routeDigest(domain), "sha256:")[:24]
}

// On a Docker Caddy host no nginx site names Caddy's copy, so the copy is the
// only pair that covers a release's domains: activation resolves to it. Left
// out of ListCertificates, every HTTPS release failed at activation with "no
// available certificate covers every deployment domain" and rolled back.
func TestDeploymentResolvesToCaddyEvidence(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	imported := useImportedDir(t)
	name := caddyEvidenceName("app.jd.test")
	certPEM, keyPEM, _ := selfSigned(t, "app.jd.test", time.Now().Add(80*24*time.Hour))
	if _, err := keepCaddyEvidence(name, certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	s := New(t.TempDir(), "")

	certPath, keyPath, err := s.ResolveDeploymentCertificate(context.Background(), []string{"app.jd.test"})
	if err != nil || certPath != filepath.Join(imported, name, "fullchain.pem") || keyPath != filepath.Join(imported, name, "privkey.pem") {
		t.Fatalf("resolved %q %q, %v", certPath, keyPath, err)
	}
	listed, _ := s.ListCertificates(context.Background())
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("the deployment list lost the copy: %+v", listed)
	}
	inventory, _ := s.CertificateInventory(context.Background())
	if len(inventory) != 0 {
		t.Fatalf("the operator's inventory lists the copy: %+v", inventory)
	}
}

// The inventory leaves out only a copy nothing serves. An import merely named
// caddy-something is an import, and a copy an nginx site names is served and
// renewed by nobody, which is worth its alarm.
func TestCertificateInventoryLeavesOutUnservedCaddyEvidence(t *testing.T) {
	imported := t.TempDir()
	served, unserved := caddyEvidenceName("served.jd.test"), caddyEvidenceName("unserved.jd.test")
	for dir, domain := range map[string]string{served: "served.jd.test", unserved: "unserved.jd.test", "caddy-shop": "shop.example.com", "bought": "bought.example.com"} {
		certPEM, _, _ := selfSigned(t, domain, time.Now().Add(10*24*time.Hour))
		if err := os.MkdirAll(filepath.Join(imported, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(imported, dir, "fullchain.pem"), []byte(certPEM), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vhosts := []VHost{{Name: "served.conf", CertPath: filepath.Join(imported, served, "fullchain.pem")}}
	all := listCertificates(filepath.Join(t.TempDir(), "live"), imported, vhosts, nil)
	if len(all) != 4 {
		t.Fatalf("listed %d, want every certificate on disk: %+v", len(all), all)
	}
	var names []string
	for _, c := range withoutCaddyEvidence(all) {
		names = append(names, c.Name)
	}
	want := []string{"bought", "caddy-shop", served}
	sort.Strings(names)
	sort.Strings(want)
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("inventory %v, want %v", names, want)
	}
}
