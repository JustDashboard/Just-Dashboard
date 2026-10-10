package proxysvc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The host's nginx binary on a private prefix serving two sites: one over
// TLS with HTTP/2, a QUIC listen advertised by Alt-Svc where the build has
// HTTP/3, a response cache and browser caching of CSS; one with a request
// limit of one a minute and a burst of one. The measurement finds each
// control doing what it says, by what nginx answered.
func TestLiveSiteControlsAreMeasured(t *testing.T) {
	root := liveNginx(t)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css")
		}
		fmt.Fprint(w, "app")
	}))
	defer app.Close()
	certPEM, keyPEM, _ := selfSigned(t, "live.test", time.Now().Add(24*time.Hour))
	for name, content := range map[string]string{"cert.pem": certPEM, "key.pem": keyPEM} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tlsPort, plainPort := freePort(t), freePort(t)
	// The site is written for the nginx at hand. A quic listen needs a build
	// with http_v3_module, which nginx has had only since 1.25, and Ubuntu
	// 24.04's 1.24 refuses one outright, along with the http2 directive.
	// Without QUIC, HTTP/3 must read as unset; every other control is measured.
	build, _ := exec.Command("nginx", "-V").CombinedOutput()
	quic := parseStreamNginxBuild(string(build)).modules["http_v3_module"] != ""
	listen := fmt.Sprintf("listen 127.0.0.1:%d ssl http2;\n", tlsPort)
	if liveNginxHasHTTP2Directive(t) {
		listen = fmt.Sprintf("listen 127.0.0.1:%d ssl;\n    http2 on;\n", tlsPort)
	}
	if quic {
		listen += fmt.Sprintf("    listen 127.0.0.1:%[1]d quic;\n    add_header Alt-Svc 'h3=\":%[1]d\"; ma=60' always;\n", tlsPort)
	}
	live := fmt.Sprintf(`proxy_cache_path %[1]s/cache levels=1:2 keys_zone=jd_live_cache:1m max_size=10m inactive=1h use_temp_path=off;
map $sent_http_content_type $jd_live_asset_expires { default off; text/css 30d; }
server {
    %[2]s    server_name live.test;
    ssl_certificate %[1]s/cert.pem;
    ssl_certificate_key %[1]s/key.pem;
    add_header X-Cache-Status $upstream_cache_status always;
    expires $jd_live_asset_expires;
    proxy_cache jd_live_cache;
    proxy_cache_valid 200 10m;
    proxy_buffering on;
    location / { proxy_pass %[3]s; }
}
`, root, listen, app.URL)
	limit := fmt.Sprintf(`limit_req_zone $binary_remote_addr zone=jd_limit_req:1m rate=1r/m;
server {
    listen 127.0.0.1:%[1]d;
    server_name limit.test;
    limit_req zone=jd_limit_req burst=1 nodelay;
    limit_req_status 429;
    location / { proxy_pass %[2]s; }
}
`, plainPort, app.URL)
	for name, content := range map[string]string{"live": live, "limit": limit} {
		available := filepath.Join(root, "sites-available", name)
		if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(available, filepath.Join(root, "sites-enabled", name)); err != nil {
			t.Fatal(err)
		}
	}
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the sites: %s", result.Output)
	}
	startNginx(t, root)
	// Waiting on the TLS site: a request to the limited one would count.
	siteGet(t, tlsPort, "live.test", "/never")

	svc := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	vhosts, err := svc.ListVHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := func(v *ControlsVerification) map[string]ControlCheck {
		out := map[string]ControlCheck{}
		for _, c := range v.Checks {
			out[c.ID] = c
		}
		return out
	}

	verified, err := svc.VerifySiteControls(ctx, "live", VerifyOptions{Path: "/page", Asset: "/style.css"}, vhosts)
	if err != nil {
		t.Fatal(err)
	}
	got := states(verified)
	measured := []string{"http2", "proxy-cache", "static-cache"}
	if quic {
		measured = append(measured, "http3")
	} else if got["http3"].State != ControlNotConfigured {
		t.Errorf("http3 without a quic listen = %+v", got["http3"])
	}
	for _, id := range measured {
		if got[id].State != ControlVerified {
			t.Errorf("%s = %+v", id, got[id])
		}
	}
	if got["rate-limit"].State != ControlNotConfigured || got["conn-limit"].State != ControlNotConfigured {
		t.Errorf("unset controls = %+v %+v", got["rate-limit"], got["conn-limit"])
	}

	limited, err := svc.VerifySiteControls(ctx, "limit", VerifyOptions{}, vhosts)
	if err != nil {
		t.Fatal(err)
	}
	if check := states(limited)["rate-limit"]; check.State != ControlVerified || !strings.Contains(strings.Join(check.Evidence, " "), "429") {
		t.Fatalf("rate limit = %+v", check)
	}
	if limited.Requests > 4 {
		t.Fatalf("the limit check sent %d requests", limited.Requests)
	}
}
