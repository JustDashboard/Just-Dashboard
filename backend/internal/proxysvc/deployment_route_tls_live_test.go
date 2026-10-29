package proxysvc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// What the host's real nginx does with a route serving one name over HTTPS
// and another over plain HTTP, with HSTS: the HTTPS name carries the
// policy in place of the application's own and sends its plain-HTTP
// visitors to HTTPS, the plain name is served as asked with neither, and
// each redirecting name answers on its own scheme and sends visitors on
// over the scheme its domain is served on. The site is written for the
// build found, without HTTP/2's directive, which nginx before 1.25.1 lacks.
func TestLiveDeploymentRouteMixedTLSOnNginx(t *testing.T) {
	root := liveNginx(t)
	ctx := context.Background()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=1")
		fmt.Fprint(w, "application answer")
	}))
	defer app.Close()
	certPEM, keyPEM, _ := selfSigned(t, "app.example.test", time.Now().Add(24*time.Hour))
	certPath, keyPath := filepath.Join(root, "app.pem"), filepath.Join(root, "app.key")
	for path, content := range map[string]string{certPath: certPEM, keyPath: keyPEM} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	route := DeploymentRoute{
		Name: "just-dashboard-env-8.conf", Domains: []string{"app.example.test", "old.example.test"}, Upstream: app.URL,
		TLS: true, ForceHTTPS: true, CertPath: certPath, KeyPath: keyPath,
		Redirects: []HostRedirect{
			{From: "www.app.example.test", To: "app.example.test"},
			{From: "www.old.example.test", To: "old.example.test"},
		},
		PlainHostnames: []string{"old.example.test", "www.old.example.test"},
		HSTS:           &DeploymentHSTS{MaxAge: 31536000, IncludeSubDomains: true},
	}
	// An unprivileged nginx cannot open the route's request record under
	// /var/log; the route is otherwise installed as activation renders it.
	spec := service.deploymentSpec(route)
	spec.AccessLog, spec.HTTP2 = false, false
	if err := service.writeMissingPages(spec); err != nil {
		t.Fatal(err)
	}
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	plainPort, tlsPort := freePort(t), freePort(t)
	for own, instead := range map[string]string{
		"    listen 80;\n":           fmt.Sprintf("    listen 127.0.0.1:%d;\n", plainPort),
		"    listen [::]:80;\n":      "",
		"    listen 443 ssl;\n":      fmt.Sprintf("    listen 127.0.0.1:%d ssl;\n", tlsPort),
		"    listen [::]:443 ssl;\n": "",
	} {
		content = strings.ReplaceAll(content, own, instead)
	}
	available := filepath.Join(root, "sites-available", route.Name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", route.Name)); err != nil {
		t.Fatal(err)
	}
	if result := runValidator(ctx, "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the route: %s\n%s", result.Output, content)
	}
	startNginx(t, root)

	get := func(scheme, host, path string) (*http.Response, string) {
		t.Helper()
		port := plainPort
		if scheme == "https" {
			port = tlsPort
		}
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: host}, DisableKeepAlives: true,
		}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, _ := http.NewRequest("GET", scheme+"://127.0.0.1:"+strconv.Itoa(port)+path, nil)
		request.Host = host
		var response *http.Response
		var err error
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if response, err = client.Do(request); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		t.Logf("%-52s → %s  Location: %q  HSTS: %q", scheme+"://"+host+path, response.Status, response.Header.Get("Location"), response.Header.Values("Strict-Transport-Security"))
		return response, string(body)
	}
	if response, body := get("https", "app.example.test", "/"); response.StatusCode != 200 || body != "application answer" {
		t.Fatalf("the HTTPS name over TLS = %s %q", response.Status, body)
	} else if got := response.Header.Values("Strict-Transport-Security"); len(got) != 1 || got[0] != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HSTS on the HTTPS name = %q, want the route's alone", got)
	}
	if response, _ := get("http", "app.example.test", "/pricing?plan=team"); response.StatusCode != 301 || response.Header.Get("Location") != "https://app.example.test/pricing?plan=team" {
		t.Fatalf("the HTTPS name over plain HTTP = %s %q", response.Status, response.Header.Get("Location"))
	}
	if response, body := get("http", "old.example.test", "/"); response.StatusCode != 200 || body != "application answer" || len(response.Header.Values("Strict-Transport-Security")) != 0 {
		t.Fatalf("the plain name was not served as asked: %s %q HSTS %q", response.Status, body, response.Header.Values("Strict-Transport-Security"))
	}
	if response, _ := get("http", "www.old.example.test", "/pricing?plan=team"); response.StatusCode != 308 || response.Header.Get("Location") != "http://old.example.test/pricing?plan=team" {
		t.Fatalf("the plain redirecting name = %s %q", response.Status, response.Header.Get("Location"))
	}
	if response, _ := get("http", "www.app.example.test", "/pricing?plan=team"); response.StatusCode != 308 || response.Header.Get("Location") != "https://app.example.test/pricing?plan=team" {
		t.Fatalf("the HTTPS redirecting name over plain HTTP = %s %q", response.Status, response.Header.Get("Location"))
	}
	if response, _ := get("https", "www.app.example.test", "/pricing?plan=team"); response.StatusCode != 308 || response.Header.Get("Location") != "https://app.example.test/pricing?plan=team" {
		t.Fatalf("the HTTPS redirecting name over TLS = %s %q", response.Status, response.Header.Get("Location"))
	}
}
