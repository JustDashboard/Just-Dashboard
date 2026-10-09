package proxysvc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The host's nginx binary on a private prefix serving two sites: one over
// TLS with HTTP/2, a QUIC listen advertised by Alt-Svc, a response cache and
// browser caching of CSS; one with a request limit of one a minute and a
// burst of one. The measurement finds each control doing what it says, by
// what nginx answered.
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
	live := fmt.Sprintf(`proxy_cache_path %[1]s/cache levels=1:2 keys_zone=jd_live_cache:1m max_size=10m inactive=1h use_temp_path=off;
map $sent_http_content_type $jd_live_asset_expires { default off; text/css 30d; }
server {
    listen 127.0.0.1:%[2]d ssl;
    listen 127.0.0.1:%[2]d quic;
    http2 on;
    server_name live.test;
    ssl_certificate %[1]s/cert.pem;
    ssl_certificate_key %[1]s/key.pem;
    add_header Alt-Svc 'h3=":%[2]d"; ma=60' always;
    add_header X-Cache-Status $upstream_cache_status always;
    expires $jd_live_asset_expires;
    proxy_cache jd_live_cache;
    proxy_cache_valid 200 10m;
    proxy_buffering on;
    location / { proxy_pass %[3]s; }
}
`, root, tlsPort, app.URL)
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
	for _, id := range []string{"http2", "http3", "proxy-cache", "static-cache"} {
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
