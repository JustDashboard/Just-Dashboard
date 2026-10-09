package proxysvc

import (
	"errors"
	"strings"
	"testing"
)

// accessTree is nginx -T of a host with the given site files enabled, the
// shared access list "office", and Cloudflare's geo file.
func accessTree(t *testing.T, sites map[string]string) []Directive {
	t.Helper()
	files := []ConfigFile{{Path: "/etc/nginx/nginx.conf", Content: "events {}\nhttp {\n    include /etc/nginx/sites-enabled/*;\n}\n"}}
	for _, name := range []string{"admin", "edge", "maint", "redirect", "sso"} {
		if content, ok := sites[name]; ok {
			files = append(files, ConfigFile{Path: "/etc/nginx/sites-enabled/" + name, Content: content})
		}
	}
	files = append(files,
		ConfigFile{Path: "/etc/nginx/jd-access/office.conf", Content: "deny 10.9.0.0/16;\nallow 10.0.0.0/8;\ndeny all;\n"},
		ConfigFile{Path: "/etc/nginx/jd-realip/cloudflare.geo", Content: "173.245.48.0/20 1;\n2400:cb00::/32 1;\n"},
	)
	tree, err := NginxTree(files)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func render(t *testing.T, spec *SiteSpec) string {
	t.Helper()
	svc := New("/etc/nginx", "/etc/caddy/Caddyfile")
	svc.SetRealIPDir(spec)
	svc.SetAccessListDir(spec)
	svc.SetPagesDir(spec)
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func layerOf(t *testing.T, a *AccessExplanation, id string) AccessLayer {
	t.Helper()
	for _, l := range a.Layers {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("no %s layer in %+v", id, a.Layers)
	return AccessLayer{}
}

const adminSite = `server {
    listen 80;
    server_name admin.example.com;
    satisfy any;
    allow 192.0.2.0/24;
    deny all;
    auth_basic "Admin";
    auth_basic_user_file /etc/nginx/jd-auth/admin;
    location / { proxy_pass http://127.0.0.1:3000; }
    location /office/ {
        include /etc/nginx/jd-access/office.conf;
        auth_basic off;
        proxy_pass http://127.0.0.1:3000;
    }
    location /status { allow all; auth_basic off; limit_req zone=jd_admin_req burst=5; proxy_pass http://127.0.0.1:3000; }
}
limit_req_zone $binary_remote_addr zone=jd_admin_req:10m rate=10r/s;
`

// satisfy any lets an allowed address through without the password and
// asks everyone else for it; a path's own list, here a shared access list,
// replaces the site's, first match deciding.
func TestExplainAccessFollowsNginxsOrderAndInheritance(t *testing.T) {
	tree := accessTree(t, map[string]string{"admin": adminSite})
	for _, tc := range []struct {
		url, source, verdict, addresses, credentials string
	}{
		{"http://admin.example.com/", "192.0.2.7", AccessAdmitted, LayerAdmits, LayerRequires},
		{"http://admin.example.com/", "198.51.100.1", AccessCredentials, LayerRefuses, LayerRequires},
		{"http://admin.example.com/office/x", "10.1.2.3", AccessAdmitted, LayerAdmits, LayerAdmits},
		{"http://admin.example.com/office/x", "10.9.0.4", AccessRefused, LayerRefuses, LayerAdmits},
		{"http://admin.example.com/office/x", "192.0.2.7", AccessRefused, LayerRefuses, LayerAdmits},
		{"http://admin.example.com/status", "198.51.100.1", AccessAdmitted, LayerAdmits, LayerAdmits},
	} {
		t.Run(tc.url+" from "+tc.source, func(t *testing.T) {
			a, err := ExplainAccess(tree, tc.url, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			if a.Verdict != tc.verdict || layerOf(t, a, "addresses").Verdict != tc.addresses || layerOf(t, a, "credentials").Verdict != tc.credentials {
				t.Fatalf("verdict %s, layers %+v (summary %q)", a.Verdict, a.Layers, a.Summary)
			}
			if layerOf(t, a, "outside").Verdict != LayerUnknown || !strings.Contains(a.Summary, "in front of this host is not seen here") {
				t.Fatalf("the outside is claimed: %+v", a)
			}
		})
	}
	a, _ := ExplainAccess(tree, "http://admin.example.com/office/x", "10.9.0.4")
	addresses := layerOf(t, a, "addresses")
	if addresses.Owner != "Access list office" || len(addresses.Rules) != 1 || addresses.Rules[0].Text != "deny 10.9.0.0/16;" ||
		addresses.Rules[0].File != "/etc/nginx/jd-access/office.conf" {
		t.Fatalf("the shared list is not named: %+v", addresses)
	}
	a, _ = ExplainAccess(tree, "http://admin.example.com/status", "198.51.100.1")
	if limit := layerOf(t, a, "rate-limit"); limit.Detail != "Requests are let through up to 10r/s per address; more are answered 429." {
		t.Fatalf("limit = %+v", limit)
	}
}

// The checks the site form writes into a server block are judged for the
// address: maintenance with its bypass list, Cloudflare only, a redirect
// that answers before any address is checked, single sign-on.
func TestExplainAccessJudgesTheFormsOwnChecks(t *testing.T) {
	maint := plainSpec("maint", "maint.example.com")
	maint.Maintenance = &SiteMaintenance{On: true, BypassFrom: []string{"192.0.2.0/24"}}
	edge := plainSpec("edge", "edge.example.com")
	edge.RealIP = &SiteRealIP{Source: "cloudflare", CloudflareOnly: true}
	redirect := plainSpec("redirect", "old.example.com")
	redirect.Kind, redirect.Upstream, redirect.RedirectTo = "redirect", "", "https://new.example.com"
	redirect.AllowFrom = []string{"10.0.0.0/8"}
	sso := plainSpec("sso", "sso.example.com")
	sso.ForwardAuth = &SiteForwardAuth{Provider: "authelia", Verify: "http://127.0.0.1:9091/api/verify", SignIn: "https://auth.example.com"}
	tree := accessTree(t, map[string]string{
		"maint": render(t, maint), "edge": render(t, edge), "redirect": render(t, redirect), "sso": render(t, sso),
	})

	for _, tc := range []struct {
		url, source, layer, layerVerdict, verdict string
	}{
		{"http://maint.example.com/", "198.51.100.1", "maintenance", LayerRefuses, AccessRefused},
		{"http://maint.example.com/", "192.0.2.9", "maintenance", LayerAdmits, AccessAdmitted},
		{"http://maint.example.com/.well-known/acme-challenge/t", "198.51.100.1", "maintenance", LayerAdmits, AccessAdmitted},
		{"http://edge.example.com/", "198.51.100.1", "edge", LayerRefuses, AccessRefused},
		{"http://edge.example.com/", "173.245.49.1", "edge", LayerAdmits, AccessAdmitted},
		{"http://edge.example.com/", "2400:cb00::1", "edge", LayerAdmits, AccessAdmitted},
		{"http://old.example.com/", "198.51.100.1", "addresses", LayerSkipped, AccessAdmitted},
		{"http://sso.example.com/", "198.51.100.1", "credentials", LayerRequires, AccessCredentials},
	} {
		t.Run(tc.url+" from "+tc.source, func(t *testing.T) {
			a, err := ExplainAccess(tree, tc.url, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			if got := layerOf(t, a, tc.layer); got.Verdict != tc.layerVerdict || a.Verdict != tc.verdict {
				t.Fatalf("%s = %+v, verdict %s (%s)", tc.layer, got, a.Verdict, a.Summary)
			}
		})
	}
	a, _ := ExplainAccess(tree, "http://sso.example.com/", "198.51.100.1")
	if detail := layerOf(t, a, "credentials").Detail; !strings.Contains(detail, "auth server behind /__jd/auth") {
		t.Fatalf("sso = %q", detail)
	}
}

// A URL nothing serves is answered as such, a firewall refusal another owner
// judges decides the answer, and a source that is not one address is
// refused before anything is read.
func TestExplainAccessEdges(t *testing.T) {
	tree := accessTree(t, map[string]string{"admin": adminSite})
	a, err := ExplainAccess(tree, "http://admin.example.com:8080/", "192.0.2.7")
	if err != nil || a.Verdict != AccessNoRoute || len(a.Layers) != 0 {
		t.Fatalf("no route = %+v, %v", a, err)
	}
	a, _ = ExplainAccess(tree, "http://admin.example.com/", "192.0.2.7")
	if host, port := a.ListenPort(); host != "" || port != 80 {
		t.Fatalf("listen = %q %d", host, port)
	}
	a.AddLayer(1, AccessLayer{ID: "firewall", Title: "Host firewall", Verdict: LayerRefuses})
	if a.Verdict != AccessRefused || a.Layers[1].ID != "firewall" || !strings.Contains(a.Summary, "Host firewall") {
		t.Fatalf("firewall refusal = %+v", a)
	}
	for _, source := range []string{"", "everyone", "10.0.0.0/8", "fe80::1%eth0"} {
		if _, err := ExplainAccess(tree, "http://admin.example.com/", source); !errors.Is(err, ErrAccessSource) {
			t.Errorf("%q: %v", source, err)
		}
	}
	if _, err := ExplainAccess(tree, "ftp://admin.example.com/", "192.0.2.7"); !errors.Is(err, ErrRouteURL) {
		t.Fatalf("ftp: %v", err)
	}
}

// A server-level if this does not know is an unknown, never a pass.
func TestExplainAccessDoesNotGuessAnIf(t *testing.T) {
	site := `server {
    listen 80;
    server_name x.example.com;
    if ($http_x_secret != "s3cret") { return 403; }
    location / { proxy_pass http://127.0.0.1:3000; }
}
`
	a, err := ExplainAccess(accessTree(t, map[string]string{"admin": site}), "http://x.example.com/", "198.51.100.1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Verdict != AccessUnknown || layerOf(t, a, "server-if").Verdict != LayerUnknown {
		t.Fatalf("= %s %+v", a.Verdict, a.Layers)
	}
}
