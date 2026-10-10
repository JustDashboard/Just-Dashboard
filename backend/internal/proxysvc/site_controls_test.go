package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A site's controls are read from its file as the form writes them, with what
// the running build lacks: an nginx.org build without the HTTP/2 and HTTP/3
// modules says so of each.
func TestSitePolicyReadsTheFormsControlsAndTheBuild(t *testing.T) {
	svc, root := provenSites(t)
	spec := plainSpec("shop", "shop.example.com")
	spec.TLS, spec.CertPath, spec.KeyPath, spec.HTTP2 = true, "/etc/ssl/shop.pem", "/etc/ssl/shop.key", true
	spec.Buffering = true
	spec.Limits = &SiteLimits{Request: &RequestLimit{Rate: "10r/s", Burst: 20, NoDelay: true}, ConnPerIP: 8, ExemptFrom: []string{}}
	spec.ProxyCache = &ProxyCache{MaxSize: "512m", Valid: "10m"}
	spec.StaticCache = &StaticCache{MaxAge: "30d"}
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	available := filepath.Join(root, "sites-available", "shop")
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", "shop")); err != nil {
		t.Fatal(err)
	}
	policy, err := svc.SitePolicy(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]PolicyControl{}
	for _, c := range policy.Controls {
		got[c.ID] = c
	}
	want := map[string]string{
		"rate-limit":   "true 10r/s, burst 20, no delay built-in",
		"conn-limit":   "true 8 at once per key built-in",
		"proxy-cache":  fmt.Sprintf("true zone %s_cache built-in", NginxIdent("shop")),
		"static-cache": fmt.Sprintf("true expires $%s_asset_expires built-in", NginxIdent("shop")),
		"http2":        "true http2 on missing",
		"http3":        "false  missing",
	}
	for id, w := range want {
		c := got[id]
		if line := fmt.Sprintf("%v %s %s", c.Configured, c.Setting, c.Support); line != w {
			t.Errorf("%s = %q, want %q", id, line, w)
		}
	}
	if policy.Engine != "nginx/1.27.5" || policy.File != available {
		t.Fatalf("policy = %+v", policy)
	}
}

// A measurement refuses a path that is not one, and a site that is not an
// enabled nginx site, before sending anything.
func TestVerifySiteControlsRefusesBeforeSending(t *testing.T) {
	svc, root := provenSites(t)
	available := filepath.Join(root, "sites-available", "shop")
	if err := os.WriteFile(available, []byte("server { listen 80; server_name shop.example.com; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vhosts := []VHost{{Name: "shop", Kind: KindNginx, Enabled: false, Listen: []string{"80"}, ServerNames: []string{"shop.example.com"}}}
	for _, opts := range []VerifyOptions{{Path: "relative"}, {Path: "/a b"}, {Asset: "x.css"}} {
		if _, err := svc.VerifySiteControls(context.Background(), "shop", opts, vhosts); !errors.Is(err, ErrVerifyPath) {
			t.Errorf("%+v: %v", opts, err)
		}
	}
	if _, err := svc.VerifySiteControls(context.Background(), "shop", VerifyOptions{}, vhosts); err == nil {
		t.Fatal("a disabled site was measured")
	}
}

func TestControlHelpers(t *testing.T) {
	for rate, want := range map[string]float64{"10r/s": 10, "30r/m": 0.5} {
		if got, ok := ratePerSecond(rate); !ok || got != want {
			t.Errorf("%s = %v %v", rate, got, ok)
		}
	}
	for _, bad := range []string{"", "10", "0r/s", "5r/h"} {
		if _, ok := ratePerSecond(bad); ok {
			t.Errorf("%q read as a rate", bad)
		}
	}
	tree, err := ParseNginxFile("/x", "server {\n limit_req zone=jd_x_req burst=3 nodelay;\n limit_req_status 429;\n limit_req_dry_run on;\n}\n", []string{"http"})
	if err != nil {
		t.Fatal(err)
	}
	if l := readLimit(tree); l.zone != "jd_x_req" || l.burst != 3 || !l.nodelay || !l.dryRun || l.status != 429 {
		t.Fatalf("limit = %+v", l)
	}
	if got := codeCounts(map[int]int{200: 3, 429: 2, 0: 1, 418: 1}); got != "3×200, 2×429, 1 failed, 1×418" && got != "3×200, 2×429, 1×418, 1 failed" {
		t.Fatalf("counts = %q", got)
	}
	host := VHost{Listen: []string{"80", "127.0.0.1:8443 ssl http2"}, ServerNames: []string{"_", "*.x.test", "app.x.test"}}
	if scheme, port, name := siteEndpoint(host); scheme != "https" || port != 8443 || name != "app.x.test" {
		t.Fatalf("endpoint = %s %d %s", scheme, port, name)
	}
	// A site on one address is reached at that address, where nginx picks
	// it, rather than on loopback, where a catch-all on the wildcard would
	// answer; a wildcard is reached on loopback in its family.
	tailnet := VHost{Listen: []string{"100.64.1.2:443 ssl", "100.64.1.2:443 quic", "[::]:80"}}
	for _, tc := range []struct {
		v    VHost
		port int
		udp  bool
		want string
	}{
		{host, 8443, false, "127.0.0.1"},
		{host, 80, false, "127.0.0.1"},
		{tailnet, 443, false, "100.64.1.2"},
		{tailnet, 443, true, "100.64.1.2"},
		{tailnet, 80, false, "::1"},
	} {
		if got := dialAddress(tc.v, tc.port, tc.udp); got != tc.want {
			t.Errorf("dialAddress(%v, %d, %v) = %s, want %s", tc.v.Listen, tc.port, tc.udp, got, tc.want)
		}
	}
}

// A hand-written file with an empty limit_conn or listen does not stop the
// reading, and a site whose name another enabled site wins on the port is
// not measured under this one's policy.
func TestSiteControlsSurviveOddFilesAndRefuseAnotherSitesName(t *testing.T) {
	svc, root := provenSites(t)
	odd := "server {\n listen;\n limit_conn;\n server_name odd.example.com;\n}\n"
	for name, content := range map[string]string{"odd": odd, "first": "server { listen 80; server_name shared.example.com; }\n", "second": "server { listen 80; server_name shared.example.com; }\n"} {
		available := filepath.Join(root, "sites-available", name)
		if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(available, filepath.Join(root, "sites-enabled", name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SitePolicy(context.Background(), "odd"); err != nil {
		t.Fatal(err)
	}
	vhosts := []VHost{
		{Name: "first", Kind: KindNginx, Enabled: true, Listen: []string{"80"}, ServerNames: []string{"shared.example.com"}},
		{Name: "second", Kind: KindNginx, Enabled: true, Listen: []string{"80"}, ServerNames: []string{"shared.example.com"}},
	}
	if _, err := svc.VerifySiteControls(context.Background(), "second", VerifyOptions{}, vhosts); err == nil || !strings.Contains(err.Error(), "from first, not second") {
		t.Fatalf("another site's name = %v", err)
	}
}
