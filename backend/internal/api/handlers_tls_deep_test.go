package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The deep scan sends forty-odd handshakes wherever it is pointed, so it is
// held to the scanner boundary, and reads its target the way the quick scan
// does.
func TestTheDeepScanIsAnAdministratorsProbe(t *testing.T) {
	s := testServer(t)
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "deep-"+string(role), role)}
		if w := c.do(http.MethodGet, "/api/v1/certificates/scan/deep?domain=127.0.0.1&port=1", "", nil); w.Code != http.StatusForbidden {
			t.Errorf("%s got %d, want 403: %s", role, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for _, path := range []string{
		"/api/v1/certificates/scan/deep?domain=",
		"/api/v1/certificates/scan/deep?domain=exa%20mple.com",
		"/api/v1/certificates/scan/deep?domain=127.0.0.1&port=0",
	} {
		if w := admin.do(http.MethodGet, path, "", nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", path, w.Code)
		}
	}
}

// Through the API, the deep scan finds the site on this server that serves
// the name and holds the negotiated protocol against its form: HTTP/2 is
// switched on there, and the server behind the port offers only HTTP/1.1.
func TestTheDeepScanHoldsHTTP2AgainstTheSiteForm(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	s := testServer(t)
	dir := t.TempDir()
	s.Cfg.NginxDir = dir
	s.initModules()
	// Without the Docker ingress lookup, which would inspect this machine's
	// own containers.
	s.modules.proxy = proxysvc.New(dir, filepath.Join(dir, "Caddyfile"))
	for _, sub := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	site := fmt.Sprintf(`server {
    listen 127.0.0.1:%d ssl;
    http2 on;
    server_name localhost;
    ssl_certificate /etc/ssl/app.pem;
    ssl_certificate_key /etc/ssl/app.key;
}
`, port)
	available := filepath.Join(dir, "sites-available", "app")
	if err := os.WriteFile(available, []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(dir, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}

	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	w := c.do(http.MethodGet, fmt.Sprintf("/api/v1/certificates/scan/deep?domain=localhost:%d", port), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var d proxysvc.DeepScan
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Reachable || d.Domain != "localhost" || d.Port != port || d.ALPN == nil ||
		d.ALPN.Negotiated != "http/1.1" || d.ALPN.Site == nil || d.ALPN.Site.Name != "app" || !d.ALPN.Site.HTTP2 {
		t.Fatalf("got %+v %+v", d, d.ALPN)
	}
	found := false
	for _, f := range d.Findings {
		found = found || f.ID == "tls.alpn.h2-off"
	}
	if !found {
		t.Errorf("findings: %+v", d.Findings)
	}
	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	for _, key := range []string{"versions", "groups", "resumption", "sni", "findings"} {
		if _, ok := raw[key].([]any); !ok {
			t.Errorf("%s should be a list, got %T", key, raw[key])
		}
	}
}
