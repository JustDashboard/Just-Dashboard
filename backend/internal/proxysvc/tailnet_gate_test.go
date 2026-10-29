package proxysvc

import (
	"strings"
	"testing"
)

func gateRoute() TailnetGateRoute {
	return TailnetGateRoute{
		Name: "just-dashboard-env-7.conf", Listen: 21003, Upstream: "http://127.0.0.1:41000",
		Check: "http://127.0.0.1:8080/api/v1/tailnet/whois?environment=7",
	}
}

// On nginx the gate listens on loopback alone, takes the tailnet address
// tailscaled forwarded as the client's, asks the dashboard's whois route
// about it with auth_request — the address in a header of its own, the
// visitor's credentials and the forwarding headers left behind — and hands
// the application the login the dashboard answered with.
func TestNginxTailnetGateAsksTheWhoisRouteWithTheClientAddress(t *testing.T) {
	content, err := renderNginxTailnetGate(gateRoute(), "/var/log/nginx/a.log", "/var/log/nginx/e.log")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		managedMarker + "\n",
		"    listen 127.0.0.1:21003;\n",
		"    set_real_ip_from 127.0.0.1;\n    set_real_ip_from ::1;\n    real_ip_header X-Forwarded-For;\n",
		"    auth_request /__jd/tailnet;\n",
		"    auth_request_set $jd_tailnet_e7_login $upstream_http_x_tailnet_login;\n",
		"    error_page 403 = @jd_tailnet_refused;\n    error_page 500 = @jd_tailnet_unavailable;\n",
		"        proxy_pass http://127.0.0.1:41000;\n",
		"        proxy_set_header X-Tailnet-Login   $jd_tailnet_e7_login;\n",
		"        proxy_set_header X-Tailnet-User    $jd_tailnet_e7_user;\n",
		"    location = /__jd/tailnet {\n        internal;\n        proxy_pass http://127.0.0.1:8080/api/v1/tailnet/whois?environment=7;\n",
		"        proxy_set_header X-Tailnet-Peer  $remote_addr;\n",
		"        proxy_set_header X-Forwarded-For \"\";\n",
		"        proxy_set_header Cookie          \"\";\n",
		"        return 503 ",
		"    access_log /var/log/nginx/a.log jd_tailnet_e7;\n",
		"tl=$jd_tailnet_e7_login';\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("the nginx gate is missing %q:\n%s", want, content)
		}
	}
	for _, never := range []string{"listen 80", "listen 443", "listen [::]", "0.0.0.0", "server_name"} {
		if strings.Contains(content, never) {
			t.Fatalf("the nginx gate carries %q:\n%s", never, content)
		}
	}
}

// On the gate Caddy the same check is forward_auth, the client address the
// one tailscaled forwarded ({client_ip}, trusted from loopback in the gate's
// own configuration), and the site bound to 127.0.0.1.
func TestCaddyTailnetGateAsksTheWhoisRouteWithTheClientAddress(t *testing.T) {
	route := gateRoute()
	route.MaxBodyMB = 32
	content, err := renderCaddyTailnetGate(route)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Managed by Just Dashboard\n",
		"http://:21003 {\n  bind 127.0.0.1\n",
		"    request_header -X-Tailnet-Login\n    request_header -X-Tailnet-User\n",
		"    forward_auth \"127.0.0.1:8080\" {\n      uri \"/api/v1/tailnet/whois?environment=7\"\n      header_up X-Tailnet-Peer {client_ip}\n",
		"      header_up -X-Forwarded-For\n",
		"      header_up -Cookie\n",
		"      copy_headers X-Tailnet-Login X-Tailnet-User\n",
		"    vars jd_tailnet_checked yes\n",
		"    log_append tailnet_login {http.request.header.X-Tailnet-Login}\n",
		"    reverse_proxy \"127.0.0.1:41000\" {\n      header_up X-Forwarded-For {client_ip}\n",
		"    max_size 32MB\n",
		"output file \"/config/just-dashboard/access/just-dashboard-env-7.log\"",
		"respond @jd_tailnet_unchecked ",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("the Caddy gate is missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, ":80 {") || strings.Contains(content, ":443") || strings.Contains(content, "tls ") || strings.Contains(content, "0.0.0.0") {
		t.Fatalf("the Caddy gate reaches past loopback:\n%s", content)
	}
	if !strings.Contains(tailnetGateConfig, "admin unix//config/caddy-admin.sock") ||
		!strings.Contains(tailnetGateConfig, "trusted_proxies static 127.0.0.1/32 ::1/128") ||
		!strings.Contains(tailnetGateConfig, "auto_https off") || !strings.Contains(tailnetGateConfig, dockerCaddyImport) {
		t.Fatalf("the gate Caddy's configuration:\n%s", tailnetGateConfig)
	}
}

// Every address the gate names is loopback, and the check is the dashboard's
// own route for the route's own environment.
func TestValidateTailnetGateHoldsEveryAddressToLoopback(t *testing.T) {
	if err := ValidateTailnetGate(gateRoute()); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*TailnetGateRoute){
		"another route's name":      func(r *TailnetGateRoute) { r.Name = "app.example.test" },
		"a privileged port":         func(r *TailnetGateRoute) { r.Listen = 443 },
		"a public upstream":         func(r *TailnetGateRoute) { r.Upstream = "http://203.0.113.7:41000" },
		"an upstream with a path":   func(r *TailnetGateRoute) { r.Upstream = "http://127.0.0.1:41000/app" },
		"another environment":       func(r *TailnetGateRoute) { r.Check = "http://127.0.0.1:8080/api/v1/tailnet/whois?environment=8" },
		"a check elsewhere":         func(r *TailnetGateRoute) { r.Check = "http://auth.example.test/api/v1/tailnet/whois?environment=7" },
		"a check on another route":  func(r *TailnetGateRoute) { r.Check = "http://127.0.0.1:8080/api/v1/auth/session?environment=7" },
		"a check with more to say":  func(r *TailnetGateRoute) { r.Check += "&x=1" },
		"a negative body limit":     func(r *TailnetGateRoute) { r.MaxBodyMB = -1 },
		"an upstream with a quote":  func(r *TailnetGateRoute) { r.Upstream = "http://127.0.0.1:41000\"" },
		"an upstream on a hostname": func(r *TailnetGateRoute) { r.Upstream = "http://localhost:41000" },
	} {
		route := gateRoute()
		edit(&route)
		if err := ValidateTailnetGate(route); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
