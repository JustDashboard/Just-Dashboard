package proxysvc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

func TestDockerCaddySelectionRequiresPublicOwnershipAndPersistence(t *testing.T) {
	raw := `[{"Id":"caddy-id","Name":"/existing-app-proxy","State":{"Running":true},"Config":{"Cmd":["caddy","run","--config","/etc/caddy/Caddyfile"]},"Mounts":[{"Type":"bind","Source":"/srv/app/Caddyfile","Destination":"/etc/caddy/Caddyfile"},{"Type":"volume","Destination":"/config","RW":true},{"Type":"volume","Destination":"/data","RW":true}],"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]}}}]`
	var containers []ingressContainer
	if err := json.Unmarshal([]byte(raw), &containers); err != nil {
		t.Fatal(err)
	}
	edge, err := selectDockerCaddy(containers)
	if err != nil || edge == nil || edge.Name != "existing-app-proxy" {
		t.Fatalf("owner=%+v error=%v", edge, err)
	}
	containers[0].NetworkSettings.Ports["80/tcp"][0].HostIP = "127.0.0.1"
	edge, err = selectDockerCaddy(containers)
	if err != nil || edge != nil {
		t.Fatal("private Caddy was selected as public ingress")
	}
	containers[0].NetworkSettings.Ports["80/tcp"][0].HostIP = "0.0.0.0"
	containers[0].Mounts = nil
	if _, err := selectDockerCaddy(containers); err == nil {
		t.Fatal("ephemeral Caddy configuration accepted")
	}
}

func TestDockerCaddyRouteRejectsInjectionAndHostPathTraversal(t *testing.T) {
	for _, domain := range []string{"app.test { respond bad }", "app.test\nadmin off", "*.example.test"} {
		if _, err := renderDockerCaddyRoute(DeploymentRoute{Domains: []string{domain}, TLS: true}, "http://127.0.0.1:8080"); err == nil {
			t.Fatalf("accepted domain %q", domain)
		}
	}
	if _, err := dockerCaddyRoutePath("just-dashboard-../../etc/Caddyfile"); err == nil {
		t.Fatal("accepted traversal")
	}
	content, err := renderDockerCaddyRoute(DeploymentRoute{Domains: []string{"app.example.test"}, TLS: false}, "http://172.17.0.2:3000")
	if err != nil || !strings.Contains(content, "http://app.example.test") || !strings.Contains(content, "172.17.0.2:3000") {
		t.Fatalf("route=%s error=%v", content, err)
	}
}

func TestLiveDockerCaddyProvisionAndRestart(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker provisioning")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	name := fmt.Sprintf("jd-ingress-provision-%d", time.Now().UnixNano())
	source := filepath.Join(t.TempDir(), "ingress", "Caddyfile")
	configVolume, dataVolume := name+"-config", name+"-data"
	defer func() {
		hostexec.Command(context.Background(), "docker", "rm", "-f", name).Run()
		hostexec.Command(context.Background(), "docker", "volume", "rm", configVolume, dataVolume).Run()
	}()
	id, err := startDockerIngress(ctx, name, source, "127.0.0.1::80", "127.0.0.1::443", configVolume, dataVolume)
	if err != nil {
		t.Fatal(err)
	}
	edge := &dockerCaddy{ID: id, Name: name, Source: source}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := edge.attach(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(source)
	if err := hostexec.Command(ctx, "docker", "stop", name).Run(); err != nil {
		t.Fatal(err)
	}
	restarted, err := startDockerIngress(ctx, name, source, "127.0.0.1::80", "127.0.0.1::443", configVolume, dataVolume)
	if err != nil || restarted != id {
		t.Fatalf("restart id=%s error=%v", restarted, err)
	}
	after, _ := os.ReadFile(source)
	if string(before) != string(after) {
		t.Fatal("restart overwrote saved configuration")
	}
	if _, err := startDockerIngress(ctx, name, source, "127.0.0.1::80", "127.0.0.1::443", configVolume+"-foreign", dataVolume); err == nil {
		t.Fatal("adopted changed storage")
	}
}

func TestLiveDockerCaddyIngressPreservesExistingSiteAndRestoresRoute(t *testing.T) {
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
	dir := t.TempDir()
	source := filepath.Join(dir, "Caddyfile")
	original := "http://existing.example.test {\n respond \"existing application\"\n}\n"
	if err := os.WriteFile(source, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	network := fmt.Sprintf("jd-ingress-test-%d", time.Now().UnixNano())
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	binding := fmt.Sprintf("127.0.0.1:%d:80", listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	tlsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsEndpoint := tlsListener.Addr().String()
	tlsBinding := tlsEndpoint + ":443"
	tlsListener.Close()
	id := docker("run", "-d", "--network", network, "-p", binding, "-p", tlsBinding, "-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	app := docker("run", "-d", "-p", "127.0.0.1::80", "nginx:alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	published := func(id string) string { return strings.TrimPrefix(docker("port", id, "80/tcp"), "127.0.0.1:") }
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-persistent-storage"}
	endpoint := "http://127.0.0.1:" + published(id)
	get := func(host string) (string, int, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", 0, err
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return string(raw), res.StatusCode, nil
	}
	for i := 0; i < 40; i++ {
		body, _, err := get("existing.example.test")
		if err == nil && strings.Contains(body, "existing application") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	service := New(dir, source)
	route := DeploymentRoute{Name: "just-dashboard-env-42.conf", Domains: []string{"candidate.example.test"}, Upstream: "http://127.0.0.1:" + published(app)}
	result, err := service.applyDockerCaddyRoute(ctx, edge, route)
	if err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v", result, err)
	}
	body, status, err := get("candidate.example.test")
	if err != nil || status != 200 || !strings.Contains(body, "Welcome to nginx") {
		t.Fatalf("candidate response=%d %q %v; fixture log: %s", status, body, err, docker("logs", id))
	}
	body, _, err = get("existing.example.test")
	if err != nil || !strings.Contains(body, "existing application") {
		t.Fatalf("existing application changed: %q %v", body, err)
	}
	saved, _ := os.ReadFile(source)
	if !strings.HasPrefix(string(saved), original) || !strings.Contains(string(saved), dockerCaddyImport) {
		t.Fatal("original configuration was not preserved")
	}
	// A new route must not take over an existing application's hostname.
	collision := route
	collision.Name, collision.Domains = "just-dashboard-collision", []string{"existing.example.test"}
	if _, err := service.applyDockerCaddyRoute(ctx, edge, collision); err == nil {
		t.Fatal("accepted existing site takeover")
	}

	// Imported certificates are copied privately and remain valid for rollback
	// after the host's import is rotated.
	certPath, keyPath := writeDeploymentCertificate(t, dir, []string{"secure.example.test"})
	secure := route
	secure.Name, secure.Domains, secure.TLS = "just-dashboard-secure", []string{"secure.example.test"}, true
	secure.CertPath, secure.KeyPath = certPath, keyPath
	tlsResult, err := service.applyDockerCaddyRoute(ctx, edge, secure)
	if err != nil || !tlsResult.Verified {
		t.Fatalf("TLS apply: %v", err)
	}
	trust := x509.NewCertPool()
	certPEM, _ := os.ReadFile(certPath)
	trust.AppendCertsFromPEM(certPEM)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trust, ServerName: "secure.example.test", MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://"+tlsEndpoint, nil)
	request.Host = "secure.example.test"
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("verified TLS handshake: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("HTTPS response=%d", response.StatusCode)
	}
	inventory, err := edge.vhosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundManaged, foundForeign := false, false
	for _, site := range inventory {
		if site.Name == route.Name && site.Enabled {
			foundManaged = true
		}
		if strings.Contains(site.Name, "existing.example.test") && site.Enabled {
			foundForeign = true
		}
	}
	if !foundManaged || !foundForeign {
		t.Fatalf("incomplete site inventory: %+v", inventory)
	}
	copiedCert, copiedKey, err := dockerCaddyCertificatePaths(certPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{copiedCert, copiedKey} {
		mode, err := edge.command(ctx, "", "stat", "-c", "%a", path)
		if err != nil || strings.TrimSpace(string(mode)) != "600" {
			t.Fatalf("certificate permissions: %q %v", mode, err)
		}
	}
	if err := edge.restore(ctx, tlsResult.Snapshot); err != nil {
		t.Fatal(err)
	}

	// A protected route asks for credentials before the application sees the
	// request: 401 without them, the application with them.
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	guarded := route
	guarded.Name, guarded.Domains = "just-dashboard-guarded", []string{"guarded.example.test"}
	guarded.BasicAuth = []BasicAuthUser{{Username: "team", Hash: string(hash)}}
	guardedResult, err := service.applyDockerCaddyRoute(ctx, edge, guarded)
	if err != nil || !guardedResult.Verified {
		t.Fatalf("protected apply=%+v error=%v", guardedResult, err)
	}
	if _, status, err := get("guarded.example.test"); err != nil || status != 401 {
		t.Fatalf("protected route without credentials: %d %v", status, err)
	}
	authed, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	authed.Host = "guarded.example.test"
	authed.SetBasicAuth("team", "correct horse battery")
	if res, err := http.DefaultClient.Do(authed); err != nil || res.StatusCode != 200 {
		t.Fatalf("protected route with credentials: %v %v", res, err)
	} else {
		res.Body.Close()
	}
	if err := edge.restore(ctx, guardedResult.Snapshot); err != nil {
		t.Fatal(err)
	}

	// A configuration saved only through the admin API must not be overwritten,
	// including after our import was previously attached.
	alternate := dockerCaddyRoot + "/api-only.caddy"
	if err := edge.write(ctx, alternate, "http://existing.example.test {\n respond \"API-only site\"\n}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := edge.command(ctx, "", "caddy", "reload", "--config", alternate, "--adapter", "caddyfile"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.applyDockerCaddyRoute(ctx, edge, route); err == nil {
		t.Fatal("overwrote API-only changes")
	}
	body, _, _ = get("existing.example.test")
	if !strings.Contains(body, "API-only site") {
		t.Fatal("API configuration was changed")
	}
	if err := edge.reload(ctx); err != nil {
		t.Fatal(err)
	}

	// Recreating an ingress loses runtime network attachments. Persistent route
	// metadata must reconnect the same application without relying on its host port.
	configVolume := docker("inspect", "--format", `{{range .Mounts}}{{if eq .Destination "/config"}}{{.Name}}{{end}}{{end}}`, id)
	dataVolume := docker("inspect", "--format", `{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}`, id)
	docker("stop", id)
	replacement := docker("run", "-d", "--network", network, "-p", binding, "-p", tlsBinding, "-v", source+":/etc/caddy/Caddyfile:ro", "-v", configVolume+":/config", "-v", dataVolume+":/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", replacement).Run()
	edge.ID = replacement
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	repairs := 0
	if err := edge.reconcile(ctx, func(name string, success bool) {
		if name != route.Name || !success {
			t.Fatalf("unexpected repair %s %v", name, success)
		}
		repairs++
	}); err != nil {
		t.Fatal(err)
	}
	if repairs != 1 {
		t.Fatalf("repairs=%d, want 1", repairs)
	}
	body, status, err = get("candidate.example.test")
	if err != nil || status != 200 || !strings.Contains(body, "Welcome to nginx") {
		t.Fatalf("recreated ingress response=%d %q %v", status, body, err)
	}
	if err := edge.reconcile(ctx, func(string, bool) { t.Fatal("unchanged route was mutated") }); err != nil {
		t.Fatal(err)
	}
	foreign := result.Snapshot
	foreign.IngressIdentity = "different-storage"
	if err := edge.restore(ctx, foreign); err == nil {
		t.Fatal("restored snapshot to different ingress")
	}
	if err := edge.restore(ctx, result.Snapshot); err != nil {
		t.Fatal(err)
	}
	_, exists, err := edge.read(ctx, result.Snapshot.Path)
	if err != nil || exists {
		t.Fatalf("candidate route survived rollback: exists=%v err=%v", exists, err)
	}
	body, _, err = get("existing.example.test")
	if err != nil || !strings.Contains(body, "existing application") {
		t.Fatal("rollback disrupted existing application")
	}
}
