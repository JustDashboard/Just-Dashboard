package proxysvc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The path a Cloudflare Tunnel's requests take on this host, without
// Cloudflare: the connector's network, the Caddy ingress joined to it, and a
// route serving one name plain for the tunnel beside one served over HTTPS
// here. A container on the tunnel's network alone — where cloudflared runs —
// asks the ingress by its container name with the visitor's Host, as
// cloudflared does, and gets the application over plain HTTP; the HTTPS
// name on the same route still sends plain HTTP to HTTPS. Joining twice is
// a no-op, and the ingress publishes nothing new for it.
func TestLiveDockerCaddyServesATunnelNamePlainToItsConnector(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker ingress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker fixture command failed: %v: %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	stamp := time.Now().UnixNano()
	dir := t.TempDir()
	source := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(source, []byte("# fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ingress := fmt.Sprintf("jdtest-ingress-%d", stamp)
	tunnel := fmt.Sprintf("jdtest-tunnel-%d", stamp)
	docker("network", "create", ingress)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", ingress).Run()
	docker("network", "create", tunnel)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", tunnel).Run()
	bindings := []string{}
	for _, port := range []string{"80", "443"} {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, "-p", fmt.Sprintf("127.0.0.1:%d:%s", listener.Addr().(*net.TCPAddr).Port, port))
		listener.Close()
	}
	name := fmt.Sprintf("jdtest-caddy-%d", stamp)
	id := docker(append(append([]string{"run", "-d", "--name", name, "--network", ingress}, bindings...), "-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")...)
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	app := docker("run", "-d", "-p", "127.0.0.1::80", "caddy:2-alpine", "caddy", "respond", "--listen", ":80", "application answer")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	edge := &dockerCaddy{ID: id, Name: name, Source: source, Identity: "fixture-persistent-storage"}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	certPEM, keyPEM, _ := selfSigned(t, "direct.tunnel.example.test", time.Now().Add(24*time.Hour))
	certPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	for path, content := range map[string]string{certPath: certPEM, keyPath: keyPEM} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// What deploymentRoute makes of a plan with one name through the tunnel
	// and one served here over HTTPS.
	route := DeploymentRoute{
		Name: "just-dashboard-env-47.conf", Domains: []string{"direct.tunnel.example.test", "shop.tunnel.example.test"},
		Upstream: "http://127.0.0.1:" + strings.TrimPrefix(docker("port", app, "80/tcp"), "127.0.0.1:"),
		TLS:      true, ForceHTTPS: true, CertPath: certPath, KeyPath: keyPath,
		PlainHostnames: []string{"shop.tunnel.example.test"},
	}
	service := New(dir, source)
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}

	origin, err := caddyTunnelOrigin(ctx, edge, tunnel)
	if err != nil || origin.Service != "http://"+name+":80" {
		t.Fatalf("origin = %+v, %v", origin, err)
	}
	if _, err := caddyTunnelOrigin(ctx, edge, tunnel); err != nil {
		t.Fatalf("joining again = %v", err)
	}
	if networks := docker("inspect", "--format", "{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}", id); !strings.Contains(networks, tunnel) || !strings.Contains(networks, ingress) {
		t.Fatalf("the ingress is on %q", networks)
	}
	// As the connector asks: by the ingress's name on the tunnel's network,
	// with the visitor's Host. busybox wget prints the status line and headers with -S, and follows a redirect, which for the HTTPS name fails on a name no resolver knows after printing it.
	ask := func(host string) string {
		raw, _ := hostexec.Command(ctx, "docker", "run", "--rm", "--network", tunnel, "caddy:2-alpine",
			"wget", "-S", "-O-", "--header", "Host: "+host, origin.Service+"/").CombinedOutput()
		return string(raw)
	}
	plain := ask("shop.tunnel.example.test")
	t.Logf("the tunnel's name, asked as cloudflared asks it:\n%s", plain)
	if !strings.Contains(plain, "200 OK") || !strings.Contains(plain, "application answer") {
		t.Fatalf("the tunnel's name was not served plain:\n%s", plain)
	}
	direct := ask("direct.tunnel.example.test")
	if !strings.Contains(direct, "308") || !strings.Contains(direct, "https://direct.tunnel.example.test/") {
		t.Fatalf("the HTTPS name on the same route over plain HTTP:\n%s", direct)
	}
	if ports := docker("port", id); strings.Count(ports, "\n")+1 != 2 {
		t.Fatalf("the ingress publishes %q", ports)
	}
}
